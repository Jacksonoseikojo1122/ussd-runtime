// Command loadtest plays many callers through a flow at once and reports response times.
//
//	go run ./cmd/loadtest -url http://localhost:8080 -gateway africastalking -c 50 -d 30s -keys 1,1,2
//
// Each virtual caller dials, presses the keys in -keys one at a time, then dials again.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

func main() {
	base := flag.String("url", "http://localhost:8080", "runtime base URL")
	gw := flag.String("gateway", "africastalking", "africastalking, hubtel, nalo or arkesel")
	conc := flag.Int("c", 50, "concurrent callers")
	dur := flag.Duration("d", 30*time.Second, "how long to run")
	keys := flag.String("keys", "1,1,2", "comma-separated inputs after dialling")
	token := flag.String("token", "", "GATEWAY_TOKEN, if the runtime needs one")
	flag.Parse()

	inputs := strings.Split(*keys, ",")
	endpoint := *base + "/ussd/" + *gw
	if *token != "" {
		endpoint += "?token=" + url.QueryEscape(*token)
	}
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: *conc * 2}}

	var (
		mu        sync.Mutex
		latencies []time.Duration
		errors    int
		sessions  int
	)
	deadline := time.Now().Add(*dur)
	var wg sync.WaitGroup
	for w := 0; w < *conc; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for time.Now().Before(deadline) {
				id := fmt.Sprintf("lt-%d-%d", w, rand.Int64())
				phone := fmt.Sprintf("23324%07d", rand.IntN(1e7))
				history := ""
				for step := 0; step <= len(inputs); step++ {
					input := ""
					if step > 0 {
						input = inputs[step-1]
						if history == "" {
							history = input
						} else {
							history += "*" + input
						}
					}
					req := build(*gw, endpoint, id, phone, step == 0, input, history)
					t := time.Now()
					resp, err := client.Do(req)
					d := time.Since(t)
					ok := err == nil && resp.StatusCode == 200
					if resp != nil {
						_, _ = io.Copy(io.Discard, resp.Body)
						resp.Body.Close()
					}
					mu.Lock()
					latencies = append(latencies, d)
					if !ok {
						errors++
					}
					mu.Unlock()
					if !ok {
						break
					}
				}
				mu.Lock()
				sessions++
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()

	slices.Sort(latencies)
	pct := func(p float64) time.Duration {
		if len(latencies) == 0 {
			return 0
		}
		return latencies[int(float64(len(latencies)-1)*p)]
	}
	ms := func(d time.Duration) string { return fmt.Sprintf("%.2f ms", float64(d.Microseconds())/1000) }
	fmt.Printf("| Gateway | Callers | Duration | Sessions | Requests | Req/s | p50 | p95 | p99 | Max | Errors |\n")
	fmt.Printf("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	fmt.Printf("| %s | %d | %s | %d | %d | %.0f | %s | %s | %s | %s | %d |\n",
		*gw, *conc, *dur, sessions, len(latencies), float64(len(latencies))/dur.Seconds(),
		ms(pct(0.50)), ms(pct(0.95)), ms(pct(0.99)), ms(pct(1)), errors)
	if errors > 0 {
		os.Exit(1)
	}
}

func build(gw, endpoint, id, phone string, first bool, input, history string) *http.Request {
	var body []byte
	ct := "application/json"
	switch gw {
	case "africastalking":
		ct = "application/x-www-form-urlencoded"
		body = []byte(url.Values{"sessionId": {id}, "phoneNumber": {"+" + phone}, "networkCode": {"62001"}, "serviceCode": {"*384*1#"}, "text": {history}}.Encode())
	case "hubtel":
		typ := "Response"
		if first {
			typ, input = "Initiation", "*713*1#"
		}
		body, _ = json.Marshal(map[string]any{"SessionId": id, "Mobile": phone, "ServiceCode": "713", "Type": typ, "Message": input, "Operator": "mtn", "Sequence": 1, "Platform": "USSD"})
	case "nalo":
		body, _ = json.Marshal(map[string]any{"USERID": "LOADTEST", "MSISDN": phone, "USERDATA": input, "MSGTYPE": first, "NETWORK": "MTN", "SESSIONID": id})
	case "arkesel":
		if first {
			input = "*928*1#"
		}
		body, _ = json.Marshal(map[string]any{"sessionID": id, "userID": "LOADTEST", "newSession": first, "msisdn": phone, "userData": input, "network": "MTN"})
	default:
		fmt.Fprintln(os.Stderr, "unknown gateway", gw)
		os.Exit(2)
	}
	req, _ := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	return req
}
