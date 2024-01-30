package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"mu.dev"

	"github.com/mmcdole/gofeed"
)

//go:embed feeds.json
var f embed.FS

var feeds = map[string]string{}

// yes I know its hardcoded
var key = os.Getenv("API_KEY")

func getPrice(v ...string) map[string]string {
	rsp, err := http.Get(fmt.Sprintf("https://min-api.cryptocompare.com/data/pricemulti?fsyms=%s&tsyms=USD&api_key=%s", strings.Join(v, ","), key))
	if err != nil {
		return nil
	}
	b, _ := ioutil.ReadAll(rsp.Body)
	defer rsp.Body.Close()
	var res map[string]interface{}
	json.Unmarshal(b, &res)
	if res == nil {
		return nil
	}
	prices := map[string]string{}
	for _, t := range v {
		rsp := res[t].(map[string]interface{})
		prices[t] = fmt.Sprintf("%v", rsp["USD"].(float64))
	}
	return prices
}

var tickers = []string{"BTC", "BNB", "ETH", "LTC"}

var replace = []string{
	"© 2024 TechCrunch. All rights reserved. For personal use only.",
}

var news = []byte{}
var mutex sync.RWMutex
var template = `
<html>
<head>
  <title>Mu News</title>
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <style>
  body { 
	  font-family: arial; 
	  font-size: 14px; 
	  color: darkslategray;
	  margin: 0 auto;
	  padding: 20px;
	  max-width: 1600px;
  }
  a { color: black; text-decoration: none; }
  .anchor {
    top: -75px;
    margin-top: 75px;
    visibility: hidden;
    position: relative;
    display: block;

  }
  #info { margin-top: 5px;}
  #nav { 
    position: sticky; top: 20; background: white;
    padding: 10px 0; overflow-x: scroll; white-space: nowrap; width: 20%%; 
    margin-right: 50px; padding-top: 100px; vertical-align: top; display: inline-block;
  }
  #news { padding-bottom: 100px; display: block; width: 70%%; display: inline-block; }
  .head { margin-right: 10px; font-weight: bold; }
  a.head { display: block; margin-bottom: 20px; }
  .section { display: block; max-width: 600px; margin-right: 20px; vertical-align: top;}
  .section img { display: none; }
  .section h3 { margin-bottom: 5px; }
  .ticker { display: block; }
  @media only screen and (max-width: 600px) {
    .section { margin-right: 0px; }
    #nav {
      position: fixed;
      padding: 20px 0 20px 0;
      margin-right: 0;
      width: 100%%;
      display: block;
      top: 0;
    }
    #news {
      width: 100%%;
      display: block;
    }
    a.head { 
      display: inline-block;
      margin-bottom: 0;
    }
    .ticker {
      display: inline-block;
      margin-right: 10px;
    }
  }
  </style>
</head>
<body>
%s
%s
</body>
</html>
`

func addHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		r.ParseForm()
		name := r.Form.Get("name")
		feed := r.Form.Get("feed")
		if len(name) == 0 || len(feed) == 0 {
			http.Error(w, "missing name or feed", 500)
			return
		}

		mutex.Lock()
		_, ok := feeds[name]
		if ok {
			mutex.Unlock()
			http.Error(w, "feed exists with name "+name, 500)
			return
		}

		// save it
		feeds[name] = feed
		mutex.Unlock()

		saveFeed()

		// redirect
		http.Redirect(w, r, "/", 302)
	}

	form := `
<h3>Add Feed</h3>
<form id="add" action="/add" method="post">
<input id="name" name="name" placeholder="feed name" required>
<br><br>
<input id="feed" name="feed" placeholder="feed url" required>
<br><br>
<button>Submit</button>
<p><small>Feed will be parsed in 1 minute</small></p>
</form>
`

	html := fmt.Sprintf(template, form, "")

	w.Write([]byte(html))
}

func saveFeed() {
	mutex.Lock()
	defer mutex.Unlock()
	file := filepath.Join(mu.Cache, "feeds.json")
	feed, _ := json.Marshal(feeds)
	os.WriteFile(file, feed, 0644)
}

func saveHtml(head, data []byte) {
	if len(data) == 0 {
		return
	}
	html := fmt.Sprintf(template, string(head), string(data))
	mutex.Lock()
	news = []byte(html)
	mutex.Unlock()
	cache := filepath.Join(mu.Cache, "news.html")
	os.WriteFile(cache, news, 0644)
}

func serveHTTP(w http.ResponseWriter, r *http.Request) {
	mutex.RLock()
	defer mutex.RUnlock()
	w.Write(news)
}

func loadFeed() {
	// load the feeds file
	data, _ := f.ReadFile("feeds.json")
	// unpack into feeds
	mutex.Lock()
	if err := json.Unmarshal(data, &feeds); err != nil {
		fmt.Println("Error parsing feeds.json", err)
	}
	mutex.Unlock()

	// load from cache
	file := filepath.Join(mu.Cache, "feeds.json")

	_, err := os.Stat(file)
	if err == nil {
		// file exists
		b, err := ioutil.ReadFile(file)
		if err == nil && len(b) > 0 {
			var res map[string]string
			json.Unmarshal(b, &res)
			mutex.Lock()
			for name, feed := range res {
				_, ok := feeds[name]
				if ok {
					continue
				}
				fmt.Println("Loading", name, feed)
				feeds[name] = feed
			}
			mutex.Unlock()
		}
	}
}

func parseFeed() {
	cache := filepath.Join(mu.Cache, "news.html")

	f, err := os.Stat(cache)
	if err == nil && len(news) == 0 {
		fmt.Println("Reading cache")
		mutex.Lock()
		news, _ = os.ReadFile(cache)
		mutex.Unlock()

		if time.Since(f.ModTime()) < time.Minute {
			time.Sleep(time.Minute)
		}
	}

	p := gofeed.NewParser()

	data := []byte{}
	head := []byte{}

	var sorted []string
	for name, _ := range feeds {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)

	for _, name := range sorted {
		feed := feeds[name]

		f, err := p.ParseURL(feed)
		if err != nil {
			fmt.Println(err)
			continue
		}

		head = append(head, []byte(`<a href="#`+name+`" class="head">`+name+`</a>`)...)

		data = append(data, []byte(`<div class=section>`)...)
		data = append(data, []byte(`<hr id="`+name+`" class="anchor">`)...)
		data = append(data, []byte(`<h1>`+name+`</h1>`)...)

		for i, item := range f.Items {
			// only 10 items
			if i >= 10 {
				break
			}

			for _, r := range replace {
				item.Description = strings.Replace(item.Description, r, "", -1)
			}

			val := fmt.Sprintf(`
<h3><a href="%s">%s</a></h2>
<span class="description">%s</span>
			`, item.Link, item.Title, item.Description)
			data = append(data, []byte(val)...)
		}

		data = append(data, []byte(`</div>`)...)
	}

	head = append(head, []byte(`<a href="/add" class="head"><button>Add Feed</button></a>`)...)
	head = append([]byte(`<div id="nav" style="z-index: 100;">`), head...)

	// get bitcoin price
	prices := getPrice(tickers...)

	if prices != nil {
		btc := prices["BTC"]
		eth := prices["ETH"]
		bnb := prices["BNB"]
		ltc := prices["LTC"]

		head = append(head, []byte(`<div id="info">`)...)
		head = append(head, []byte(`<span class="ticker">btc $`+btc+`</span>`)...)
		head = append(head, []byte(`<span class="ticker">eth $`+eth+`</span>`)...)
		head = append(head, []byte(`<span class="ticker">bnb $`+bnb+`</span>`)...)
		head = append(head, []byte(`<span class="ticker">ltc $`+ltc+`</span>`)...)
		head = append(head, []byte(`</div>`)...)
	}

	head = append(head, []byte(`</div>`)...)

	data = append([]byte(`<div id="news">`), data...)
	data = append(data, []byte(`</div>`)...)

	saveHtml(head, data)

	time.Sleep(time.Minute)
	parseFeed()
}

func main() {
	port := "8080"

	if v := os.Getenv("PORT"); len(v) > 0 {
		port = v
	}

	// load the feeds
	loadFeed()

	go parseFeed()

	http.HandleFunc("/", serveHTTP)
	http.HandleFunc("/add", addHandler)
	http.ListenAndServe(":"+port, nil)
}
