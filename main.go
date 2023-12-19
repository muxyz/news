package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mmcdole/gofeed"
)

var feeds = map[string]string{
	"UK":     "https://feeds.bbci.co.uk/news/rss.xml",
	"World":  "https://www.aljazeera.com/xml/rss/all.xml",
	"Market": "https://www.ft.com/news-feed?format=rss",
	"Tech":   "https://techcrunch.com/feed/",
	"Dev":    "https://news.ycombinator.com/rss",
}

var replace = []string{
	"© 2023 TechCrunch. All rights reserved. For personal use only.",
}

var news = []byte{}
var mutex sync.RWMutex
var template = `
<html>
<head>
  <style>
  body { 
	  padding: 25px; 
	  font-family: arial; 
	  font-size: 14px; 
	  color: darkslategray;
	  width: 1400px;
	  margin: 0 auto;
  }
  a { color: black; text-decoration: none; }
  #nav { padding: 20px 0; }
  .head { margin-right: 10px }
  hr { margin: 50px 0; }
  </style>
</head>
<body>
%s
%s
</body>
</html>
`

func serveHTTP(w http.ResponseWriter, r *http.Request) {
	mutex.RLock()
	defer mutex.RUnlock()

	w.Write(news)
}

func parseFeed() {
	f, err := os.Stat("news.html")
	if err == nil && len(news) == 0 {
		fmt.Println("Reading file")
		mutex.Lock()
		news, _ = os.ReadFile("news.html")
		mutex.Unlock()
		fmt.Println(string(news))

		if time.Since(f.ModTime()) < time.Minute {
			time.Sleep(time.Minute)
		}
	}

	p := gofeed.NewParser()

	data := []byte{}
	head := []byte{}

	for name, feed := range feeds {
		f, err := p.ParseURL(feed)
		if err != nil {
			fmt.Println(err)
			continue
		}

		head = append(head, []byte(`<a href="#`+name+`" class="head">`+name+`</a>`)...)
		data = append(data, []byte(`<hr id="`+name+`">`)...)
		data = append(data, []byte(`<h1>`+name+" - "+f.Title+`</h1>`)...)
		if f.Title != f.Description {
			data = append(data, []byte(`<h2>`+f.Description+`</h2>`)...)
		}

		for i, item := range f.Items {
			// only 10 items
			if i >= 5 {
				break
			}

			for _, r := range replace {
				item.Description = strings.Replace(item.Description, r, "", -1)
			}

			val := fmt.Sprintf(`
<h3><a href="%s">%s</a></h2>
<p>%s</p>
			`, item.Link, item.Title, item.Description)
			data = append(data, []byte(val)...)
		}
	}

	head = append([]byte(`<div id="nav" style="position: fixed; top: 0; z-index: 100; background: white; width: 100%;">`), head...)
	head = append(head, []byte(`</div>`)...)

	data = append([]byte(`<div id="news">`), data...)
	data = append(data, []byte(`</div>`)...)

	writeHtml(head, data)

	time.Sleep(time.Minute)
	parseFeed()
}

func writeHtml(head, data []byte) {
	if len(data) == 0 {
		return
	}
	html := fmt.Sprintf(template, string(head), string(data))
	mutex.Lock()
	news = []byte(html)
	mutex.Unlock()
	os.WriteFile("news.html", news, 0644)
}

func main() {
	port := "8080"

	if v := os.Getenv("PORT"); len(v) > 0 {
		port = v
	}

	go parseFeed()

	http.HandleFunc("/", serveHTTP)
	http.ListenAndServe(":"+port, nil)
}
