package main

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/disk"
	"github.com/spf13/pflag"
)

// DiskInfo holds information about a disk partition
type DiskInfo struct {
	Device      string
	Size        uint64
	Used        uint64
	Free        uint64
	Type        string
	HumanSize   string
	HumanUsed   string
	HumanFree   string
	UsedPercent float64
	FreePercent float64
}

// humanReadableSize converts a size in bytes to a human-readable string
func humanReadableSize(size uint64) string {
	const unit = 1024

	if size < unit {
		return fmt.Sprintf("%d B", size)
	}

	div, exp := int64(unit), 0

	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}

// gatherDiskInfo collects disk information, ignoring specified types
func gatherDiskInfo(ignoreTypes []string, hostProc string) []DiskInfo {
	partitions, err := disk.PartitionsWithContext(context.Background(), true)
	if err != nil {
		log.Printf("Error retrieving partitions: %v", err)
		return nil
	}

	var diskInfos []DiskInfo

	for _, p := range partitions {
		// Check if the partition has a mount point
		if p.Mountpoint == "" {
			log.Printf("Partition %s has no mountpoint, skipping", p.Device)
			continue
		}

		usage, err := disk.Usage(filepath.Join(hostProc, p.Mountpoint))
		if err != nil {
			log.Printf("Error getting usage for %s: %v", p.Mountpoint, err)
			continue
		}

		if usage.Total == 0 {
			log.Printf("Partition %s has total size 0, skipping", p.Mountpoint)
			continue
		}

		if contains(ignoreTypes, p.Fstype) {
			log.Printf("Partition %s with type %s is in ignore list, skipping", p.Mountpoint, p.Fstype)
			continue
		}

		usedPercent := usage.UsedPercent
		freePercent := 100 - usage.UsedPercent

		diskInfos = append(diskInfos, DiskInfo{
			Device:      p.Device,
			Size:        usage.Total,
			Used:        usage.Used,
			Free:        usage.Free,
			Type:        p.Fstype,
			HumanSize:   humanReadableSize(usage.Total),
			HumanUsed:   humanReadableSize(usage.Used),
			HumanFree:   humanReadableSize(usage.Free),
			UsedPercent: usedPercent,
			FreePercent: freePercent,
		})
	}

	return diskInfos
}

// contains checks if a slice contains a specific string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// renderTemplate parses and executes a template with provided data
func renderTemplate(w http.ResponseWriter, tmpl string, data interface{}) {
	t, err := template.ParseFS(staticFs, "web/templates/"+tmpl)
	if err != nil {
		log.Printf("template parsing error: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	err = t.Execute(w, data)
	if err != nil {
		log.Printf("template execution error: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handler returns an http.HandlerFunc that renders the disk information page
func handler(ignoreTypes []string, hostProc string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := struct {
			DiskInfos []DiskInfo
		}{
			DiskInfos: gatherDiskInfo(ignoreTypes, hostProc),
		}
		renderTemplate(w, "index.html", data)
	}
}

// multiStringFlag is a custom flag type for handling multiple string flags
type multiStringFlag []string

func (m *multiStringFlag) String() string {
	return strings.Join(*m, ",")
}

func (m *multiStringFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}

func (m *multiStringFlag) Type() string {
	return "string"
}

//go:embed web
var staticFs embed.FS

func main() {
	var ignoreTypes multiStringFlag
	envIgnoreTypes := os.Getenv("DISKINFO_IGNORE_TYPES")
	if envIgnoreTypes != "" {
		ignoreTypes = strings.Split(envIgnoreTypes, ",")
	}
	pflag.VarP(&ignoreTypes, "ignore-type", "i", "File system types to ignore (can be specified multiple times). You can also use the DISKINFO_IGNORE_TYPES environment variable.")

	var hostProc string
	envHostProc := os.Getenv("DISKINFO_HOST_PROC")
	if envHostProc == "" {
		hostProc = "/proc"
	} else {
		hostProc = envHostProc
	}

	pflag.StringVarP(&hostProc, "host-proc", "p", hostProc, "Path to the host's /proc directory. You can also use the DISKINFO_HOST_PROC environment variable.")
	help := pflag.BoolP("help", "h", false, "Show help message")

	// Override the default usage function to include custom environment variable information
	pflag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		pflag.PrintDefaults()
		fmt.Fprintln(os.Stderr, "\nYou can also use the DISKINFO_IGNORE_TYPES and DISKINFO_HOST_PROC environment variables.")
	}

	pflag.Parse()

	if *help {
		pflag.Usage()
		os.Exit(0)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handler(ignoreTypes, hostProc))

	// Only this ugly way worked with the correct mime type
	fsys := fs.FS(staticFs)
	contentStatic, _ := fs.Sub(fsys, "web/static")
	staticHandler := http.StripPrefix("/static/", http.FileServer(http.FS(contentStatic)))
	mux.Handle("/static/", staticHandler)

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	// Channel to listen for signals
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// Goroutine to start the server
	go func() {
		log.Println("Starting server on :8080")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("ListenAndServe(): %s", err)
		}
	}()

	<-stop // Blocking call waiting for shutdown signal

	log.Println("Shutting down gracefully...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server Shutdown Failed:%+v", err)
	}

	log.Println("Server exited")
}
