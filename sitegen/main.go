package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/russross/blackfriday/v2"
	"golang.org/x/tools/blog/atom"
	"sigs.k8s.io/yaml"
)

func main() {
	o, err := newOptions()
	if err != nil {
		log.Fatal(err)
	}

	err = o.processPages()
	if err != nil {
		log.Fatal(err)
	}

	err = o.convertImgs()
	if err != nil {
		log.Fatal(err)
	}

	// err = o.signExchanges()
	// if err != nil {
	// 	log.Fatal(err)
	// }

	err = o.deploy()
	if err != nil {
		log.Fatal(err)
	}
}

type Page struct {
	URLCanonical string
	URLAMP       string
	AMP          bool
	GAID         string

	Title       string
	Description string
	Style       string
	Header      string
	Main        string

	Date  string     // blogpost
	Posts []BlogPost // blogindex
}

func (p *Page) setAMP() {
	p.AMP = true
}

type BlogPost struct {
	Date  string
	Title string
	URL   string
}

func (o options) processPages() error {
	var err error
	var blogindex Page
	var sitemapPages []string
	var blogPosts []BlogPost

	err = filepath.Walk(o.src, func(fp string, fi os.FileInfo, ierr error) error {
		if fi.IsDir() {
			return nil
		} else if filepath.Ext(fp) == ".md" {
			if strings.HasSuffix(fp, "blog/index.md") {
				_, blogindex, err = o.parsePage(fp)
				return nil
			}
			urls, bp, err := o.processPage(fp)
			if err != nil {
				return fmt.Errorf("options.processPages: %w", err)
			}
			sitemapPages = append(sitemapPages, urls...)
			if bp != nil {
				blogPosts = append(blogPosts, *bp)
			}
		} else {
			o.copyFile(fp, nil)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("options.processPages: %w", err)
	}

	sort.Slice(blogPosts, func(i, j int) bool { return blogPosts[i].URL > blogPosts[j].URL })
	blogindex.Posts = blogPosts

	// generate sitemap, blog index, atom feed
	err = o.writeTemplate("/blog/index.html", "layout-blogindex", &blogindex)
	if err != nil {
		return fmt.Errorf("options.processPages: %w", err)
	}

	u, err := o.atomFeed(blogPosts)
	if err != nil {
		return fmt.Errorf("options.processPages: %w", err)
	}
	sitemapPages = append(sitemapPages, u)

	sort.Strings(sitemapPages)
	err = ioutil.WriteFile(filepath.Join(o.dst, "sitemap.txt"), []byte(strings.Join(sitemapPages, "\n")), 0644)
	if err != nil {
		return fmt.Errorf("options.processPages: %w", err)
	}

	return nil
}

// processPage takes a filepath from current directory
// and creates the cprresponding filepath.html and amp/filepath.html
// also sends the relative url path segments to collect
func (o options) processPage(fp string) (urls []string, bp *BlogPost, err error) {
	fps, p, err := o.parsePage(fp)
	if err != nil {
		return nil, nil, fmt.Errorf("options.processPage: %w", err)
	}

	htmlpath := filepath.Join(fps[1:]...) + ".html"
	if fps[1] == "blog" {
		err = o.writeTemplate(htmlpath, "layout-blogpost", &p)
		if err != nil {
			return nil, nil, fmt.Errorf("options.processPage: %w", err)
		}
		if p.Title == "" {
			fmt.Println(fp)
		}
		bp = &BlogPost{
			Title: p.Title,
			Date:  p.Date,
			URL:   strings.TrimSuffix(fps[len(fps)-1], ".html"),
		}
	} else {
		err = o.writeTemplate(htmlpath, "layout-main", &p)
		if err != nil {
			return nil, nil, fmt.Errorf("options.processPage: %w", err)
		}
	}
	return []string{p.URLCanonical, p.URLAMP}, bp, nil
}

// parsePage takes a filepath from the current directory
// and returns a the path segments and a processed page
func (o options) parsePage(fp string) ([]string, Page, error) {
	fps, p := strings.Split(fp, "/"), Page{GAID: o.gaID}
	fps[0], fps[len(fps)-1] = "amp", strings.TrimSuffix(fps[len(fps)-1], ".md")
	u, _ := url.Parse(o.baseURL)
	u.Path = filepath.Join(fps[1:]...)
	p.URLCanonical = normalizeURL(u.String())
	u.Path = filepath.Join(fps...)
	p.URLAMP = normalizeURL(u.String())

	b, err := ioutil.ReadFile(fp)
	if err != nil {
		return nil, p, fmt.Errorf("parsePage: %s %v", fp, err)
	}
	if bytes.HasPrefix(b, []byte(`---`)) {
		parts := bytes.SplitN(b, []byte(`---`), 3)
		err := yaml.Unmarshal(parts[1], &p)
		if err != nil {
			return nil, p, fmt.Errorf("parsePage: front matter %v", err)
		}
		b = parts[2]
	}

	p.Main = string(blackfriday.Run(
		b,
		blackfriday.WithRenderer(
			blackfriday.NewHTMLRenderer(
				blackfriday.HTMLRendererParameters{
					Flags: blackfriday.CommonHTMLFlags,
				},
			),
		),
	))
	if fps[1] == "blog" && fps[2] != "index" {
		p.Date = fps[len(fps)-1][:10]
	}
	return fps, p, nil
}

func (o *options) atomFeed(bps []BlogPost) (string, error) {
	me := &atom.Person{
		Name:  "Sean Liao",
		URI:   "https://seankhliao.com",
		Email: "blog-atom@seankhliao.com",
	}

	fd := atom.Feed{
		Title: "seankhliao's stream of consciousness",
		ID:    "tag:seankhliao.com,2019:seankhliao.com",
		Link: []atom.Link{
			{
				Rel:  "self",
				Href: "https://seankhliao.com/feed.atom",
				Type: "application/atom+xml",
			}, {
				Rel:  "alternate",
				Href: "https://seankhliao.com/blog",
				Type: "text/html",
			},
		},
		Updated: atom.Time(time.Now()),
		Author:  me,
	}
	u, _ := url.Parse(o.baseURL)
	for _, bp := range bps {
		u.Path = filepath.Join("blog", bp.URL)
		htmlpath := normalizeURL(u.String())
		u.Path = filepath.Join("amp", u.Path)
		amppath := normalizeURL(u.String())
		fd.Entry = append(fd.Entry, &atom.Entry{
			Title: bp.Title,
			Link: []atom.Link{
				{
					Rel:  "alternate",
					Href: htmlpath,
					Type: "text/html",
				},
				{
					Rel:  "amphtml",
					Href: amppath,
					Type: "text/html",
				},
			},
			ID:        htmlpath,
			Published: atom.TimeStr(bp.Date + "T00:00:00Z"),
			Updated:   atom.TimeStr(bp.Date + "T00:00:00Z"),
			Author:    me,
			Summary: &atom.Text{
				Type: "text",
				Body: bp.Title,
			},
		})
	}
	f, err := os.Create(filepath.Join(o.dst, "feed.atom"))
	if err != nil {
		return "", fmt.Errorf("options.atomFeed: %w", err)
	}
	defer f.Close()
	e := xml.NewEncoder(f)
	e.Indent("", "    ")
	err = e.Encode(fd)
	if err != nil {
		return "", fmt.Errorf("options.atomFeed: %w", err)
	}
	u.Path = "feed.atom"
	return u.String(), nil
}
