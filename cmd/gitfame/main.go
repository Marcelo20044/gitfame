package main

import (
	"fmt"
	"gitfame/config"
	"gitfame/internal/app"
	"log"
	"os"
	"time"
)

func main() {
	cfg, err := config.ParseConfig()
	if err != nil {
		log.Fatalf("failed to parse cfg: %v", err)
	}

	collector := app.NewStatsCollector(cfg)

	processWithLoading(func() {
		if err = collector.CollectStats(); err != nil {
			log.Fatalf("\nfailed to collect statistics: %v", err)
		}
	}, "Collecting statistics")

	if err = collector.PrintStats(); err != nil {
		log.Fatalf("\nfailed to print statistics: %v", err)
	}
}

// processWithLoading outputs spinner with loadingSign to stderr while processing function f
func processWithLoading(f func(), loadingSign string) {
	collected := make(chan struct{})
	go func() {
		f()
		collected <- struct{}{}
	}()

	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

	frameIndex := 0
	for {
		select {
		case <-collected:
			fmt.Println()
			return
		default:
			if _, err := fmt.Fprintf(os.Stderr, "\r%s %s", loadingSign, frames[frameIndex]); err != nil {
				log.Fatalf("\nfailed to print spinner: %v", err)
			}
			frameIndex = (frameIndex + 1) % len(frames)
			time.Sleep(80 * time.Millisecond)
		}
	}
}
