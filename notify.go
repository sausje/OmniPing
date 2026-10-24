package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Notice is what a channel delivers.
type Notice struct {
	Kind   string // start, each, end, ok, ipchange, test
	Title  string
	Body   string
	Target Target
	Rule   Rule
	Reason string
	Time   int64
}

// Notifier delivers notices in the background with a few retries.
type Notifier struct {
	app    *App
	queue  chan *job
	client *http.Client
}

type job struct {
	ch      Channel
	n       Notice
	done    func(error) // called once, with the final result
	retry   func(error) // called after each failed attempt that will be retried
	attempt int
	first   time.Time
}

// retryAfter is the wait before each next attempt; the last one repeats. A
// notification that cannot get out (the internet is down, say) is retried
// for retryFor, so it still arrives once the connection is back.
var retryAfter = []time.Duration{3 * time.Second, 20 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute}

const retryFor = 6 * time.Hour

func newNotifier(app *App) *Notifier {
	n := &Notifier{app: app, queue: make(chan *job, 512), client: &http.Client{Timeout: 15 * time.Second}}
	for range 4 {
		go n.worker()
	}
	return n
}

// Send queues a notice. done is called with the final result; retry, if not
// nil, after each failed attempt that will be tried again.
func (n *Notifier) Send(ch Channel, notice Notice, done, retry func(error)) {
	n.enqueue(&job{ch: ch, n: notice, done: done, retry: retry, first: time.Now()})
}

func (n *Notifier) enqueue(j *job) {
	select {
	case n.queue <- j:
	default:
		if j.done != nil {
			j.done(errors.New("too many notifications queued"))
		}
	}
}

// SendNow delivers immediately (used by the Test buttons).
func (n *Notifier) SendNow(ch Channel, notice Notice) error {
	return n.deliver(ch, notice)
}

func (n *Notifier) worker() {
	for j := range n.queue {
		err := n.deliver(j.ch, j.n)
		if err != nil && !isPermanent(err) && time.Since(j.first) < retryFor {
			wait := retryAfter[min(j.attempt, len(retryAfter)-1)]
			j.attempt++
			if j.attempt == 1 || j.attempt%10 == 0 {
				log.Printf("notification via %q failed, trying again: %v", j.ch.Name, err)
			}
			if j.retry != nil {
				j.retry(err)
			}
			time.AfterFunc(wait, func() { n.enqueue(j) })
			continue
		}
		if err != nil {
			log.Printf("notification via %q failed: %v", j.ch.Name, err)
		}
		if j.done != nil {
			j.done(err)
		}
	}
}

type permanentError struct{ error }

func isPermanent(err error) bool {
	var p permanentError
	return errors.As(err, &p)
}

func (n *Notifier) deliver(ch Channel, nt Notice) error {
	c := ch.Config
	switch ch.Type {
	case "browser":
		n.app.hub.Broadcast("notify", map[string]any{
			"title": nt.Title, "body": nt.Body, "kind": nt.Kind, "target": nt.Target.ID,
			"sound": c["sound"] != "off", "channel": ch.ID,
		})
		return nil
	case "ntfy":
		return n.ntfy(c, nt)
	case "telegram":
		if c["token"] == "" || c["chatId"] == "" {
			return permanentError{errors.New("bot token and chat ID are required")}
		}
		body, _ := json.Marshal(map[string]any{"chat_id": c["chatId"], "text": nt.Title + "\n" + nt.Body, "disable_web_page_preview": true})
		return n.post("https://api.telegram.org/bot"+c["token"]+"/sendMessage", "application/json", body, nil)
	case "discord":
		if c["url"] == "" {
			return permanentError{errors.New("webhook URL is required")}
		}
		body, _ := json.Marshal(map[string]any{"content": "**" + nt.Title + "**\n" + nt.Body})
		return n.post(c["url"], "application/json", body, nil)
	case "pushover":
		if c["token"] == "" || c["user"] == "" {
			return permanentError{errors.New("application token and user key are required")}
		}
		form := url.Values{"token": {c["token"]}, "user": {c["user"]}, "title": {nt.Title}, "message": {nt.Body}}
		if nt.Kind == "start" {
			form.Set("priority", "1")
		}
		return n.post("https://api.pushover.net/1/messages.json", "application/x-www-form-urlencoded", []byte(form.Encode()), nil)
	case "gotify":
		if c["server"] == "" || c["token"] == "" {
			return permanentError{errors.New("server and application token are required")}
		}
		prio := 5
		if nt.Kind == "start" {
			prio = 8
		}
		body, _ := json.Marshal(map[string]any{"title": nt.Title, "message": nt.Body, "priority": prio})
		return n.post(strings.TrimRight(c["server"], "/")+"/message?token="+url.QueryEscape(c["token"]), "application/json", body, nil)
	case "webhook":
		return n.webhook(c, nt)
	case "email":
		return sendMail(c, nt.Title, nt.Body)
	case "log":
		return n.logLine(c, nt)
	case "script":
		return n.script(c, nt)
	case "windows":
		return n.desktop(nt)
	}
	return permanentError{fmt.Errorf("unknown channel type %q", ch.Type)}
}

// errNoDesktop is the "Windows notification" channel outside the Windows app.
var errNoDesktop = errors.New("Windows notifications only work in the Windows app")

// desktop shows the notice as a Windows notification. Clicking it opens
// the target, or the alerts page. Tests show the red icon too, so they look
// like the real thing.
func (n *Notifier) desktop(nt Notice) error {
	if !n.app.desktop() {
		return permanentError{errNoDesktop}
	}
	path := "#/alerts"
	switch {
	case nt.Kind == "summary":
		path = "#/report"
	case nt.Target.ID != "":
		path = "#/target/" + nt.Target.ID
	}
	alert := nt.Kind == "start" || nt.Kind == "each" || nt.Kind == "test"
	n.app.shell.Notify(nt.Title, nt.Body, path, alert)
	return nil
}

func (n *Notifier) post(u, contentType string, body []byte, headers map[string]string) error {
	return n.request(http.MethodPost, u, contentType, body, headers)
}

func (n *Notifier) request(method, u, contentType string, body []byte, headers map[string]string) error {
	req, err := http.NewRequest(method, u, bytes.NewReader(body))
	if err != nil {
		return permanentError{err}
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("User-Agent", "OmniPing/"+version)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode >= 300 {
		err := fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(msg)))
		if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != 429 {
			return permanentError{err}
		}
		return err
	}
	return nil
}

func (n *Notifier) ntfy(c map[string]string, nt Notice) error {
	server := strings.TrimRight(c["server"], "/")
	if server == "" {
		server = "https://ntfy.sh"
	}
	if c["topic"] == "" {
		return permanentError{errors.New("topic is required")}
	}
	h := map[string]string{"Title": mimeHeader(nt.Title)}
	switch nt.Kind {
	case "start", "each", "ipchange":
		h["Tags"] = "warning"
		if p := c["priority"]; p != "" {
			h["Priority"] = p
		} else {
			h["Priority"] = "high"
		}
	case "end", "ok":
		h["Tags"] = "white_check_mark"
	case "summary":
		h["Tags"] = "bar_chart"
	default:
		h["Tags"] = "test_tube"
	}
	if tok := c["token"]; tok != "" {
		h["Authorization"] = "Bearer " + tok
	}
	return n.post(server+"/"+url.PathEscape(c["topic"]), "text/plain; charset=utf-8", []byte(nt.Body), h)
}

// mimeHeader keeps non-ASCII titles intact in HTTP headers.
func mimeHeader(s string) string {
	for _, r := range s {
		if r > 127 {
			return mime.QEncoding.Encode("utf-8", s)
		}
	}
	return s
}

func (n *Notifier) webhook(c map[string]string, nt Notice) error {
	if c["url"] == "" {
		return permanentError{errors.New("URL is required")}
	}
	method := strings.ToUpper(c["method"])
	if method == "" {
		method = http.MethodPost
	}
	body, _ := json.Marshal(map[string]any{
		"event": nt.Kind, "title": nt.Title, "message": nt.Body, "reason": nt.Reason,
		"target": nt.Target.Name, "targetId": nt.Target.ID, "host": nt.Target.Host, "ip": nt.Target.IP,
		"group": nt.Target.Group, "rule": nt.Rule.Name, "time": time.UnixMilli(nt.Time).Format(time.RFC3339),
	})
	if method == http.MethodGet {
		body = nil
	}
	return n.request(method, c["url"], "application/json", body, nil)
}

func (n *Notifier) logLine(c map[string]string, nt Notice) error {
	p, err := logPath(n.app.dataDir, c["path"])
	if err != nil {
		return permanentError{err}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	line := strings.Join([]string{
		time.UnixMilli(nt.Time).Format("2006-01-02 15:04:05"), nt.Kind, nt.Target.Name, nt.Target.IP,
		nt.Rule.Name, strings.ReplaceAll(nt.Body, "\n", " "),
	}, "\t")
	_, err = fmt.Fprintln(f, line)
	return err
}

// errScriptsOff explains how to turn the script channel on.
var errScriptsOff = errors.New("running scripts is turned off; start OmniPing with OMNIPING_ALLOW_SCRIPTS=true to allow it")

func (n *Notifier) script(c map[string]string, nt Notice) error {
	if !n.app.allowScripts {
		return permanentError{errScriptsOff}
	}
	cmdline := strings.TrimSpace(c["command"])
	if cmdline == "" {
		return permanentError{errors.New("command is required")}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := shellCommand(ctx, cmdline)
	cmd.Dir = n.app.dataDir
	cmd.Env = append(os.Environ(),
		"OMNIPING_EVENT="+nt.Kind, "OMNIPING_TITLE="+nt.Title, "OMNIPING_MESSAGE="+nt.Body,
		"OMNIPING_REASON="+nt.Reason, "OMNIPING_TARGET="+nt.Target.Name, "OMNIPING_HOST="+nt.Target.Host,
		"OMNIPING_IP="+nt.Target.IP, "OMNIPING_GROUP="+nt.Target.Group, "OMNIPING_RULE="+nt.Rule.Name,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return permanentError{fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))}
	}
	return nil
}

func sendMail(c map[string]string, subject, body string) error {
	host := c["host"]
	if host == "" || c["from"] == "" || c["to"] == "" {
		return permanentError{errors.New("SMTP server, from and to are required")}
	}
	port := c["port"]
	security := c["security"]
	if port == "" {
		port = map[string]string{"tls": "465", "none": "25"}[security]
		if port == "" {
			port = "587"
		}
	}
	addr := net.JoinHostPort(host, port)
	var conn net.Conn
	var err error
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	if security == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: host})
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return err
	}
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	cl, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return err
	}
	defer cl.Close()
	if security != "tls" && security != "none" {
		if ok, _ := cl.Extension("STARTTLS"); !ok {
			return permanentError{errors.New("server does not offer STARTTLS; choose another security setting")}
		}
		if err := cl.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return err
		}
	}
	if user := c["username"]; user != "" {
		if err := cl.Auth(smtp.PlainAuth("", user, c["password"], host)); err != nil {
			return permanentError{err}
		}
	}
	if err := cl.Mail(c["from"]); err != nil {
		return permanentError{err}
	}
	var to []string
	for _, a := range strings.Split(c["to"], ",") {
		if a = strings.TrimSpace(a); a != "" {
			to = append(to, a)
			if err := cl.Rcpt(a); err != nil {
				return permanentError{err}
			}
		}
	}
	w, err := cl.Data()
	if err != nil {
		return err
	}
	msg := "From: " + c["from"] + "\r\n" +
		"To: " + strings.Join(to, ", ") + "\r\n" +
		"Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n" +
		"Date: " + time.Now().Format(time.RFC1123Z) + "\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n" +
		"X-Mailer: OmniPing " + version + "\r\n\r\n" +
		strings.ReplaceAll(body, "\n", "\r\n") + "\r\n"
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return cl.Quit()
}
