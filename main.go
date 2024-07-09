package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/mmcdole/gofeed"
)

type DiscordMessage struct {
	Content string `json:"content"`
}

type RssConfig struct {
	FeedUrl string `toml:"feed_url"`
}

type DiscordConfig struct {
	WebhookUrl string `toml:"webhook_url"`
}

type ConfigTreeRoot struct {
	Discord       DiscordConfig `toml:"discord"`
	Rss           RssConfig     `toml:"rss"`
	TimestampFile string        `toml:"timestamp_file"`
}

func main() {
	var (
		configPath string
	)
	flag.StringVar(&configPath, "config", "", "path to the configuration file")
	flag.Parse()

	config := new(ConfigTreeRoot)
	if _, err := toml.DecodeFile(configPath, config); err != nil {
		log.Fatalf("cannot parse config file: %v", err)
	}

	var f *os.File
	f, err := os.OpenFile(config.TimestampFile, os.O_RDWR, 0644)
	if !os.IsExist(err) {
		f, err = os.OpenFile(config.TimestampFile, os.O_RDWR|os.O_CREATE, 0644)
	}
	if err != nil {
		log.Fatalf("cannot open timestamp file: %v", err)
		return
	}
	defer f.Close()

	h := NewHandler(f, config)

	lastRun, err := h.readLastPostTime()
	if err != nil {
		log.Println("Error reading last run time:", err)
		last := time.Now().Add(-3 * time.Hour)
		lastRun = &last
	}

	fp := gofeed.NewParser()
	feed, err := fp.ParseURL(config.Rss.FeedUrl)
	if err != nil {
		log.Println("Error parsing RSS feed:", err)
		return
	}

	var newestPostTime time.Time
	for _, item := range feed.Items {
		if lastRun != nil && !item.PublishedParsed.After(*lastRun) {
			continue
		}
		time.Sleep(time.Second * 1)
		err := h.sendToDiscord(item.Title + ": " + item.Link)
		if err != nil {
			log.Fatalf("Error sending to Discord: %v", err)
			continue
		}
		log.Println("posted:", item.Link)

		if item.PublishedParsed.After(newestPostTime) {
			newestPostTime = *item.PublishedParsed
		}
	}

	if !newestPostTime.IsZero() {
		if err = h.saveLastPostTime(newestPostTime); err != nil {
			fmt.Println("Error saving last post time:", err)
		}
	}
}

type Handler struct {
	TimestampFile *os.File
	Config        *ConfigTreeRoot
}

func NewHandler(f *os.File, config *ConfigTreeRoot) *Handler {
	return &Handler{
		TimestampFile: f,
		Config:        config,
	}
}

func (h *Handler) readLastPostTime() (*time.Time, error) {
	buf := new(bytes.Buffer)
	_, err := buf.ReadFrom(h.TimestampFile)
	if err != nil {
		return nil, err
	}
	t, err := time.Parse(time.RFC3339, buf.String())
	fmt.Println("t: ", t)
	return &t, err
}

func (h *Handler) saveLastPostTime(t time.Time) error {
	if err := h.TimestampFile.Truncate(0); err != nil {
		return err
	}
	_, err := io.WriteString(h.TimestampFile, t.Format(time.RFC3339))
	return err
}

func (h *Handler) sendToDiscord(content string) error {
	msg := &DiscordMessage{
		Content: content,
	}
	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(msg); err != nil {
		return err
	}
	fmt.Println(h.Config.Discord.WebhookUrl, buf.String())

	resp, err := http.Post(h.Config.Discord.WebhookUrl, "application/json", buf)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}
