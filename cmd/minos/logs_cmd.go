package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// logRecord mirrors applog.Record as the API serves it.
type logRecord struct {
	Seq     uint64    `json:"seq"`
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
	Attrs   []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	} `json:"attrs"`
}

// clientLogs prints a running instance's application log — the same records
// the UI's Logs page shows, secrets already redacted — and with -f keeps
// printing new ones as they arrive.
func clientLogs(args []string) error {
	fs, cfgPath, _ := parseCommon("logs", args)
	follow := fs.Bool("f", false, "keep printing new records as they arrive")
	level := fs.String("level", "", "only this level and above: debug, info, warn, error")
	n := fs.Int("n", 100, "how many of the newest records to print first (0 = all held)")
	fs.Usage = func() {
		_, _ = fmt.Fprintln(fs.Output(), "usage: minos logs [-f] [-level warn] [-n 100] [-config minos.yaml]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	base, authorize, err := apiClient(*cfgPath)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	fetch := func(after uint64) ([]logRecord, error) {
		q := url.Values{}
		if *level != "" {
			q.Set("level", *level)
		}
		if after > 0 {
			q.Set("after", fmt.Sprint(after))
		}
		req, err := http.NewRequest(http.MethodGet, base+"/api/logs?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		authorize(req)
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("is minos running? %w", err)
		}
		defer resp.Body.Close()
		var out struct {
			Records []logRecord `json:"records"`
			Error   string      `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return nil, fmt.Errorf("bad response: %w", err)
		}
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("%s", out.Error)
		}
		return out.Records, nil
	}

	recs, err := fetch(0)
	if err != nil {
		return err
	}
	if *n > 0 && len(recs) > *n {
		recs = recs[len(recs)-*n:]
	}
	var last uint64
	for _, r := range recs {
		printLogRecord(r)
		last = r.Seq
	}
	for *follow {
		time.Sleep(time.Second)
		recs, err := fetch(last)
		if err != nil {
			return err
		}
		for _, r := range recs {
			printLogRecord(r)
			last = r.Seq
		}
	}
	return nil
}

func printLogRecord(r logRecord) {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %-5s %s", r.Time.Local().Format("2006-01-02 15:04:05"), strings.ToUpper(r.Level), r.Message)
	for _, a := range r.Attrs {
		v := a.Value
		if v == "" || strings.ContainsAny(v, " \t\"=") {
			v = fmt.Sprintf("%q", v)
		}
		fmt.Fprintf(&b, " %s=%s", a.Key, v)
	}
	fmt.Println(b.String())
}
