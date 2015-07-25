package main

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jpillora/backoff"
	"github.com/jpillora/opts"
	"github.com/shirou/gopsutil/load"
	"github.com/shirou/gopsutil/mem"
)

var VERSION = "0"

type config struct {
	URL      string        `help:"InfluxDB URL"`
	Database string        `help:"InfluxDB database"`
	Interval time.Duration `help:"Time between reports"`
	Tags     string        `help:"InfluxDB tags in the form \"<tag>=<value>,<tag>=<value>\""`
}

func main() {
	c := config{
		URL:      "http://localhost:8086",
		Database: "test",
		Interval: 5 * time.Minute,
	}

	opts.New(&c).Name("sysflux").Version(VERSION).Parse()

	//validate config
	u, err := url.Parse(c.URL)
	if err != nil || u.Host == "" {
		log.Fatal("Invalid URL")
	}
	if u.Path == "" {
		u.Path = "/write"
	}
	v := url.Values{}
	v.Set("db", c.Database)
	u.RawQuery = v.Encode()

	tags := ""
	if c.Tags != "" {
		tags = "," + c.Tags
	}

	//good to go
	log.Printf("Using InfluxDB endpoint: %s", u)

	b := backoff.Backoff{Max: 2 * c.Interval}
	entries := ""
	for {
		e := bytes.Buffer{}
		if l, err := load.LoadAvg(); err == nil {
			fmt.Fprintf(&e, "\ncpu_load_short%s value=%f", tags, l.Load1)
			fmt.Fprintf(&e, "\ncpu_load_medium%s value=%f", tags, l.Load5)
			fmt.Fprintf(&e, "\ncpu_load_long%s value=%f", tags, l.Load15)
		}
		if v, err := mem.VirtualMemory(); err == nil {
			fmt.Fprintf(&e, "\nmem_usage%s value=%f", tags, v.UsedPercent)
		}
		entries += e.String()

		if len(entries) > 0 {
			resp, err := http.Post(u.String(), "application/x-www-form-urlencoded", strings.NewReader(entries))
			if err != nil || resp.StatusCode != http.StatusNoContent {
				if err != nil {
					log.Printf("HTTP POST failed: %s", err)
				}
				time.Sleep(b.Duration()) //wait a little extra
			} else {
				entries = "" //success - clear body
				b.Reset()
			}
		}
		time.Sleep(c.Interval)
	}
}
