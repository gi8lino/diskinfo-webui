package main

import (
	"fmt"
	"html/template"
	"math"
	"net/http"
	"strings"

	"github.com/shirou/gopsutil/disk"
)

// DiskInfo holds information about a disk
type DiskInfo struct {
	Device      string
	Size        uint64
	Used        uint64
	Free        uint64
	HumanSize   string
	HumanUsed   string
	HumanFree   string
	UsedPercent int
	FreePercent int
	PieChart    string
}

var (
	diskInfos   []DiskInfo
	ignoreDisks []string
)

// humanReadable converts bytes to a human-readable string
func humanReadable(bytes uint64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// generatePieChart generates an SVG path for a pie chart representing the disk usage
func generatePieChart(usedPercent int) string {
	radians := float64(usedPercent) / 100 * 2 * math.Pi
	x := 50 + 50*math.Sin(radians)
	y := 50 - 50*math.Cos(radians)
	largeArcFlag := 0
	if usedPercent > 50 {
		largeArcFlag = 1
	}
	path := fmt.Sprintf("M50,50 L50,0 A50,50 0 %d,1 %.2f,%.2f L50,50 Z", largeArcFlag, x, y)
	return path
}

// FetchDiskInfo fetches disk information
func FetchDiskInfo() {
	partitions, err := disk.Partitions(false)
	if err != nil {
		fmt.Println("Error fetching partitions:", err)
		return
	}

	diskInfos = nil

	for _, p := range partitions {
		usage, err := disk.Usage(p.Mountpoint)
		if err != nil {
			fmt.Println("Error fetching usage for", p.Device, ":", err)
			continue
		}
		for _, idisk := range ignoreDisks {
			if strings.Contains(p.Device, idisk) {
				fmt.Printf("Ignore disk '%s'", idisk)
			}
		}
		usedPercent := int((float64(usage.Used) / float64(usage.Total)) * 100)
		freePercent := 100 - usedPercent
		pieChart := generatePieChart(usedPercent)
		diskInfos = append(diskInfos, DiskInfo{
			Device:      p.Device,
			Size:        usage.Total,
			Used:        usage.Used,
			Free:        usage.Free,
			HumanSize:   humanReadable(usage.Total),
			HumanUsed:   humanReadable(usage.Used),
			HumanFree:   humanReadable(usage.Free),
			UsedPercent: usedPercent,
			FreePercent: freePercent,
			PieChart:    pieChart,
		})
	}
}

// IndexHandler handles the main page
func IndexHandler(w http.ResponseWriter, r *http.Request) {
	FetchDiskInfo()

	view := r.URL.Query().Get("view")
	if view == "" {
		view = "table"
	}

	tmpl := template.Must(template.ParseFiles("templates/index.html"))
	tmpl.Execute(w, map[string]interface{}{
		"DiskInfos": diskInfos,
		"View":      view,
	})
}

func main() {
	http.HandleFunc("/", IndexHandler)
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	fmt.Println("Starting server at :8080")
	http.ListenAndServe(":8080", nil)
}
