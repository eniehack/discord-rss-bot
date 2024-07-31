package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
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
		slog.Error("cannot parse config file", "msg", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	h := NewHandler(config, logger)

	lastRun, err := h.readLastPostTime()
	if err != nil && lastRun == nil {
		slog.Info("timestamp file not found")
		last := time.Now().Add(-2 * time.Hour)
		lastRun = &last
	} else if err != nil {
		slog.Error("Error last run time reading from file", "err", err)
		os.Exit(1)
	}

	fp := gofeed.NewParser()
	feed, err := fp.ParseURL(config.Rss.FeedUrl)
	if err != nil {
		slog.Error("Error parsing RSS feed", "msg", err)
		os.Exit(1)
	}

	var newestPostTime time.Time
	for _, item := range feed.Items {
		if lastRun != nil && !item.PublishedParsed.After(*lastRun) {
			continue
		}
		time.Sleep(time.Second * 1)
		err := h.sendToDiscord(item.Title + ": " + item.Link)
		if err != nil {
			slog.Warn("Error sending message to Discord", "msg", err)
			continue
		}
		slog.Debug("posted a feed", "msg", fmt.Sprintf("link: %s", item.Link))

		if item.PublishedParsed.After(newestPostTime) {
			newestPostTime = *item.PublishedParsed
		}
	}

	if newestPostTime.IsZero() {
		slog.Warn("newestPostTime is zero")
		os.Exit(1)
	}
	if err = h.saveLastPostTime(newestPostTime); err != nil {
		slog.Error("Error saving last post time", "msg", err)
	}
}

type Handler struct {
	Config *ConfigTreeRoot
}

func NewHandler(config *ConfigTreeRoot, logger *slog.Logger) *Handler {
	return &Handler{
		Config: config,
	}
}

func (h *Handler) readLastPostTime() (*time.Time, error) {
	var f *os.File
	f, err := os.Open(h.Config.TimestampFile)
	if os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		slog.Error("cannot open timestamp file", "msg", err)
		return nil, err
	}
	defer f.Close()

	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(f)
	if err != nil {
		return nil, err
	}
	crTrimedStr := strings.TrimRight(buf.String(), "\n")
	t, err := time.Parse(time.RFC3339, crTrimedStr)
	slog.Debug("parsed time", "msg", fmt.Sprintf("timestamp: %s", t))
	return &t, err
}

func (h *Handler) saveLastPostTime(t time.Time) error {
	var f *os.File
	f, err := os.OpenFile(h.Config.TimestampFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.WriteString(f, t.Format(time.RFC3339)+"\n")
	return err
}

func (h *Handler) sendToDiscord(content string) error {
	msg := h.createMessage(content)
	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(msg); err != nil {
		return err
	}
	slog.Debug("created POST body", "msg", buf.String())

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

func (h *Handler) createMessage(content string) *DiscordMessage {
	return &DiscordMessage{
		Content: content,
	}
}
