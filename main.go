package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jpillora/backoff"
	"github.com/jpillora/opts"
	"github.com/miekg/dns"
	"github.com/shirou/gopsutil/load"
	"github.com/shirou/gopsutil/mem"
)

var VERSION = "0"

const MAX_QUEUED = 10e3

type config struct {
	URL      string        `help:"InfluxDB URL"`
	Database string        `help:"InfluxDB database"`
	Interval time.Duration `help:"Time between reports"`
	Tags     string        `help:"InfluxDB tags in the form \"<tag>=<value>,<tag>=<value>\""`
	DNS      string        `help:"DNS server (used to lookup URL)"`
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
	log.Printf("Current time is %s UTC", time.Now().Format(time.RFC3339))

	lock := sync.Mutex{}
	entries := []string{}

	send := func() error {
		body := strings.NewReader(strings.Join(entries, "\n"))
		if c.DNS != "" {
			h, p, _ := net.SplitHostPort(u.Host)
			ips, err := lookup(h, c.DNS)
			if err != nil {
				return fmt.Errorf("Lookup failed: %s", err)
			}
			u.Host = ips[0] + ":" + p
		}
		resp, err := http.Post(u.String(), "application/x-www-form-urlencoded", body)
		if err != nil {
			return fmt.Errorf("HTTP POST failed: %s", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			msg, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				return fmt.Errorf("Response download failed: %s", err)
			}
			return fmt.Errorf("Response: %d => %s", resp.StatusCode, msg)
		}
		log.Printf("Success")
		//clear once recieved!
		entries = nil
		return nil
	}

	report := func() {
		t := time.Now().UnixNano()
		if l, err := load.LoadAvg(); err == nil {
			entries = append(entries, fmt.Sprintf("cpu_load_short%s value=%f %d", tags, l.Load1*100, t))
			entries = append(entries, fmt.Sprintf("cpu_load_medium%s value=%f %d", tags, l.Load5*100, t))
			entries = append(entries, fmt.Sprintf("cpu_load_long%s value=%f %d", tags, l.Load15*100, t))
		}
		if v, err := mem.VirtualMemory(); err == nil {
			entries = append(entries, fmt.Sprintf("mem_usage%s value=%f %d", tags, v.UsedPercent, t))
		}
		if len(entries) > MAX_QUEUED {
			entries = entries[len(entries)-MAX_QUEUED:]
		}
	}

	//send loop
	go func() {
		b := backoff.Backoff{}
		for {
			wait := time.Second
			lock.Lock()
			if len(entries) > 0 {
				if err := send(); err == nil {
					b.Reset()
				} else {
					log.Println(err)
					wait = b.Duration()
				}
			}
			lock.Unlock()
			time.Sleep(wait)
		}
	}()

	//report loop
	for {
		lock.Lock()
		report()
		lock.Unlock()
		time.Sleep(c.Interval)
	}
}

var dnsClient = dns.Client{}

func lookup(domain, server string) ([]string, error) {
	msg := dns.Msg{}
	msg.SetQuestion(domain+".", dns.TypeA)
	r, _, err := dnsClient.Exchange(&msg, server+":53")
	if err != nil {
		return nil, err
	}
	l := len(r.Answer)
	if l == 0 {
		return nil, fmt.Errorf("No answers")
	}
	ips := make([]string, l)
	for i, answer := range r.Answer {
		ans := answer.(*dns.A)
		ips[i] = ans.A.String()
	}
	return ips, nil
}
