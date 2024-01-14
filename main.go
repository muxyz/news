package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mmcdole/gofeed"
)

var Files string

func init() {
	user, err := os.UserHomeDir()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	home := filepath.Join(user, "mu")
	if err := os.MkdirAll(home, 0700); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	files := filepath.Join(home, "cache")
	if err := os.MkdirAll(files, 0700); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	// set bin
	Files = files
}

var feeds = map[string]string{
	"Dev":    "https://news.ycombinator.com/rss",
	"UK":     "https://feeds.bbci.co.uk/news/rss.xml",
	"World":  "https://www.aljazeera.com/xml/rss/all.xml",
	"Market": "https://www.ft.com/news-feed?format=rss",
	"Tech":   "https://techcrunch.com/feed/",
}

var replace = []string{
	"© 2023 TechCrunch. All rights reserved. For personal use only.",
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
	  max-width: 600px;
	  margin: 0 auto;
	  padding: 20px;
  }
  a { color: black; text-decoration: none; }
  #nav { padding: 20px 0; }
  #news { padding-bottom: 100px; }
  .head { margin-right: 10px; font-weight: bold; }
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
	cache := filepath.Join(Files, "news.html")

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
		data = append(data, []byte(`<hr id="`+name+`">`)...)
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
	cache := filepath.Join(Files, "news.html")
	os.WriteFile(cache, news, 0644)
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
