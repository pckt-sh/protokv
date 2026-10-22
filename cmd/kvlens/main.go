// Command kvlens browses the messages stored with package kv in a terminal.
//
// Models are read from FileDescriptorSets or buf images:
//
//	buf build -o models.binpb
//	KV_URL=redis://localhost:6666 KVLENS_DESCRIPTORS=models.binpb kvlens
package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/pckt-sh/protokv/kv"
	"github.com/redis/go-redis/v9"
)

// quietLogger drops go-redis logs, they would be printed over the TUI.
type quietLogger struct{}

func (quietLogger) Printf(context.Context, string, ...any) {}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "kvlens:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		dbURL       = flag.String("url", firstEnv("KV_URL", "KVROCKS_URL", "REDIS_URL"), "database URL, defaults to $KV_URL, $KVROCKS_URL or $REDIS_URL")
		descriptors = flag.String("descriptors", os.Getenv("KVLENS_DESCRIPTORS"), "comma-separated FileDescriptorSet or buf image files, defaults to $KVLENS_DESCRIPTORS")
	)
	flag.Parse()

	redis.SetLogger(quietLogger{})

	if *dbURL == "" {
		return fmt.Errorf("no database URL, set KV_URL or -url")
	}

	if *descriptors == "" {
		return fmt.Errorf("no models, build them with `buf build -o models.binpb` and set KVLENS_DESCRIPTORS or -descriptors")
	}

	var paths []string
	for p := range strings.SplitSeq(*descriptors, ",") {
		if p = strings.TrimSpace(p); p != "" {
			paths = append(paths, filepath.Clean(p))
		}
	}

	if err := loadDescriptors(paths); err != nil {
		return err
	}

	db, err := kv.NewClient(*dbURL)
	if err != nil {
		return err
	}
	defer db.Conn().Close()

	_, err = tea.NewProgram(newApp(db, redactURL(*dbURL), storedModels())).Run()
	return err
}

func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}

	return ""
}

// redactURL hides the password shown in the header.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}

	return u.Redacted()
}
