package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
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

var status = map[string]*Feed{}

// yes I know its hardcoded
var key = os.Getenv("API_KEY")

type Feed struct {
	Name     string
	URL      string
	Error    error
	Attempts int
	Backoff  time.Time
}

type Article struct {
	Title       string
	Description string
	URL         string
	Published   string
	Category    string
	PostedAt    time.Time
}

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

var tickers = []string{"BTC", "BNB", "ETH", "SOL"}

var replace = []func(string) string{
	func(v string) string {
		return strings.Replace(v, "© 2024 TechCrunch. All rights reserved. For personal use only.", "", -1)
	},
	func(v string) string {
		return regexp.MustCompile(`<img .*>`).ReplaceAllString(v, "")
	},
}

var news = []byte{}
var mutex sync.RWMutex

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
<h1>Add Feed</h1>
<form id="add" action="/add" method="post">
<input id="name" name="name" placeholder="feed name" required>
<br><br>
<input id="feed" name="feed" placeholder="feed url" required>
<br><br>
<button>Submit</button>
<p><small>Feed will be parsed in 1 minute</small></p>
</form>
`

	html := mu.Template("Add Feed", "Add a news feed", "", form)

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
	html := mu.Template("News", "Read the news", string(head), string(data))
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

func statusHandler(w http.ResponseWriter, r *http.Request) {
	mutex.RLock()
	b, _ := json.Marshal(status)
	mutex.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
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
	urls := map[string]string{}
	stats := map[string]Feed{}

	var sorted []string

	mutex.RLock()
	for name, url := range feeds {
		sorted = append(sorted, name)
		urls[name] = url

		if stat, ok := status[name]; ok {
			stats[name] = *stat
		}
	}
	mutex.RUnlock()

	sort.Strings(sorted)

	var headlines []*Article

	for _, name := range sorted {
		feed := urls[name]

		// check last attempt
		stat, ok := stats[name]
		if !ok {
			stat = Feed{
				Name: name,
				URL:  feed,
			}

			mutex.Lock()
			status[name] = &stat
			mutex.Unlock()
		}

		// it's a reattempt, so we need to check what's going on
		if stat.Attempts > 0 {
			// there is still some time on the clock
			if time.Until(stat.Backoff) > time.Duration(0) {
				// skip this iteration
				continue
			}

			// otherwise we've just hit our threshold
			fmt.Println("Reattempting pull of", feed)
		}

		// parse the feed
		f, err := p.ParseURL(feed)
		if err != nil {
			// up the attempts
			stat.Attempts++
			// set the error
			stat.Error = err
			// set the backoff
			stat.Backoff = time.Now().Add(mu.Backoff(stat.Attempts))
			// print the error
			fmt.Printf("Error parsing %s: %v, attempt %d backoff until %v", feed, err, stat.Attempts, stat.Backoff)

			mutex.Lock()
			status[name] = &stat
			mutex.Unlock()

			// skip ahead
			continue
		}

		mutex.Lock()
		// successful pull
		stat.Attempts = 0
		stat.Backoff = time.Time{}
		stat.Error = nil

		// readd
		status[name] = &stat
		mutex.Unlock()

		head = append(head, []byte(`<a href="#`+name+`" class="head">`+name+`</a>`)...)

		data = append(data, []byte(`<div class=section>`)...)
		data = append(data, []byte(`<hr id="`+name+`" class="anchor">`)...)
		data = append(data, []byte(`<h1>`+name+`</h1>`)...)

		for i, item := range f.Items {
			// only 10 items
			if i >= 10 {
				break
			}

			for _, fn := range replace {
				item.Description = fn(item.Description)
			}

			val := fmt.Sprintf(`
<h3><a href="%s" rel="noopener noreferrer" target="_blank">%s</a></h3>
<span class="description">%s</span>
			`, item.Link, item.Title, item.Description)
			data = append(data, []byte(val)...)

			if i > 0 {
				continue
			}

			headlines = append(headlines, &Article{
				Title:       item.Title,
				Description: item.Description,
				URL:         item.Link,
				Published:   item.Published,
				PostedAt:    *item.PublishedParsed,
				Category:    name,
			})
		}

		data = append(data, []byte(`</div>`)...)
	}

	// head = append(head, []byte(`<a href="/add" class="head"><button>Add</button></a>`)...)

	headline := []byte(`<div class=section><hr id="headlines" class="anchor">`)

	// get crypto prices
	prices := getPrice(tickers...)

	if prices != nil {
		btc := prices["BTC"]
		eth := prices["ETH"]
		bnb := prices["BNB"]
		sol := prices["SOL"]

		var info []byte
		info = append(info, []byte(`<div id="info">`)...)
		info = append(info, []byte(`<span class="ticker">btc $`+btc+`</span>`)...)
		info = append(info, []byte(`<span class="ticker">eth $`+eth+`</span>`)...)
		info = append(info, []byte(`<span class="ticker">bnb $`+bnb+`</span>`)...)
		info = append(info, []byte(`<span class="ticker">sol $`+sol+`</span>`)...)
		info = append(info, []byte(`</div>`)...)
		headline = append(headline, info...)
	}

	headline = append(headline, []byte(`<h1>Headlines</h1>`)...)

	// create the headlines
	sort.Slice(headlines, func(i, j int) bool {
		return headlines[i].PostedAt.After(headlines[j].PostedAt)
	})

	for _, h := range headlines {
		val := fmt.Sprintf(`
			<div class="headline"><a href="#%s" class="category">%s</a><h3><a href="%s" rel="noopener noreferrer" target="_blank">%s</a></h3><span class="description">%s</span></div>`,
			h.Category, h.Category, h.URL, h.Title, h.Description)
		headline = append(headline, []byte(val)...)
	}

	headline = append(headline, []byte(`</div>`)...)

	// set the headline
	data = append(headline, data...)


	// save it
	saveHtml(head, data)

	// wait 10 minutes
	time.Sleep(time.Minute * 5)

	// go again
	parseFeed()
}

func feedsHandler(w http.ResponseWriter, r *http.Request) {
	mutex.RLock()
	defer mutex.RUnlock()

	data := `<h1>Feeds</h1>`

	for name, feed := range feeds {
		data += fmt.Sprintf(`<a href="%s">%s</a><br>`, feed, name)
	}

	html := mu.Template("Feeds", "News RSS feeds", "", data)
	w.Write([]byte(html))
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
	http.HandleFunc("/feeds", feedsHandler)
	http.HandleFunc("/status", statusHandler)
	http.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`User-agent: *
Allow: /`))
	})
	http.ListenAndServe(":"+port, nil)
}
