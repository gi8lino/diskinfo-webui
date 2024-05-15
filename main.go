package main

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/disk"
)

type DiskInfo struct {
	Device         string
	Size           uint64
	Used           uint64
	Free           uint64
	Type           string
	HumanSize      string
	HumanUsed      string
	HumanFree      string
	UsedPercent    float64
	FreePercent    float64
	PieChart       string
	UsedTextX      float64
	UsedTextY      float64
	FreeTextX      float64
	FreeTextY      float64
	UsedPercentStr string
	FreePercentStr string
}

var (
	templates   *template.Template
	ignoreTypes = []string{"cdrom"}
)

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

func formatPercent(percent float64) string {
	return fmt.Sprintf("%.0f", percent)
}

func calculateTextPosition(percent float64, radius float64) (float64, float64) {
	angle := (percent / 100) * 360.0
	rad := angle * (math.Pi / 180.0)
	x := 50 + radius*math.Cos(rad) // assuming the center is at (50, 50)
	y := 50 - radius*math.Sin(rad) // SVG coordinates are inverted for y

	// Apply a slight offset to the x coordinate to move the text left or right
	offset := 15.0
	if percent > 50 {
		x -= offset
	} else {
		x += offset
	}

	return x, y
}

func calculatePieChartPath(percent float64) string {
	if percent == 0 {
		return ""
	}
	r := 50.0
	x := 50 + r*math.Cos(2*math.Pi*percent/100-0.5*math.Pi)
	y := 50 + r*math.Sin(2*math.Pi*percent/100-0.5*math.Pi)
	largeArc := 0
	if percent > 50 {
		largeArc = 1
	}
	return fmt.Sprintf("M50,50 L50,0 A50,50 0 %d,1 %.2f,%.2f Z", largeArc, x, y)
}

func stringIsInSliceOfStrings(s string, list []string) bool {
	for _, element := range list {
		if element == s {
			return true
		}
	}
	return false
}

func gatherDiskInfo() []DiskInfo {
	partitions, _ := disk.Partitions(true)
	var diskInfos []DiskInfo

	for _, p := range partitions {
		usage, _ := disk.Usage(p.Mountpoint)
		if usage.Total == 0 {
			continue
		}

		if stringIsInSliceOfStrings(p.Fstype, ignoreTypes) {
			continue
		}

		usedPercent := usage.UsedPercent
		freePercent := 100 - usage.UsedPercent

		di := DiskInfo{
			Device:         p.Device,
			Size:           usage.Total,
			Used:           usage.Used,
			Free:           usage.Free,
			Type:           p.Fstype,
			HumanSize:      humanReadableSize(usage.Total),
			HumanUsed:      humanReadableSize(usage.Used),
			HumanFree:      humanReadableSize(usage.Free),
			UsedPercent:    usedPercent,
			FreePercent:    freePercent,
			UsedPercentStr: formatPercent(usedPercent),
			FreePercentStr: formatPercent(freePercent),
			PieChart:       calculatePieChartPath(usedPercent),
		}

		if di.UsedPercent > 0 && di.UsedPercent < 100 {
			di.UsedTextX, di.UsedTextY = calculateTextPosition(di.UsedPercent/2, 35)
		} else {
			di.UsedTextX, di.UsedTextY = 50, 50
		}

		if di.FreePercent > 0 && di.FreePercent < 100 {
			di.FreeTextX, di.FreeTextY = calculateTextPosition(100-(di.FreePercent/2), 35)
		} else {
			di.FreeTextX, di.FreeTextY = 50, 50
		}

		diskInfos = append(diskInfos, di)
	}

	return diskInfos
}

func renderTemplate(w http.ResponseWriter, tmpl string, data interface{}) {
	err := templates.ExecuteTemplate(w, tmpl+".html", data)
	if err != nil {
		log.Printf("template execution error: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func handler(w http.ResponseWriter, r *http.Request) {
	view := r.URL.Query().Get("view")
	if view == "" {
		view = "table"
	}
	data := struct {
		View      string
		DiskInfos []DiskInfo
	}{
		View:      view,
		DiskInfos: gatherDiskInfo(),
	}
	renderTemplate(w, "index", data)
}

func main() {
	templates = template.Must(template.ParseFiles("templates/index.html"))

	mux := http.NewServeMux()
	mux.HandleFunc("/", handler)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

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
