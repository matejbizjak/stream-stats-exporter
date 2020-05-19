package main

import (
	"fmt"
	vlc "github.com/adrg/libvlc-go/v3"
	"github.com/tatsushid/go-fastping"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "net/http/pprof"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/log"
	"github.com/prometheus/common/version"
	"gopkg.in/alecthomas/kingpin.v2"
)

const (
	namespace = "monitoring"
)

var (
	listenAddress = kingpin.Flag("web.listen-address", "Address to listen on for web interface and telemetry.").Default(":8080").String()
	metricsPath   = kingpin.Flag("web.telemetry-path", "Path under which to expose metrics.").Default("/metrics").String()
	//streamingTime = kingpin.Flag("analysis.streaming-time", "Time of the connection to the stream.").Default("5").Duration()

	// Metrics about the Stream Stats Exporter itself.
	ssDuration = prometheus.NewSummary(prometheus.SummaryOpts{Name: prometheus.BuildFQName(namespace, "exporter", "duration_seconds"), Help: "Duration of collections by the Stream Stats Exporter."})
	ssErrors   = prometheus.NewCounter(prometheus.CounterOpts{Name: prometheus.BuildFQName(namespace, "exporter", "errors_total"), Help: "Errors raised by the Stream Stats Exporter."})
)

// Exporter collects network stats from the given address and exports them using
// the prometheus metrics package.
type Exporter struct {
	target        string
	period        time.Duration
	streamingTime int
	mutex         sync.RWMutex

	success *prometheus.Desc
	bitrate *prometheus.Desc
	latency *prometheus.Desc
}

// NewExporter returns an initialized Exporter.
func NewExporter(target string, period time.Duration, streamingTime int) *Exporter {
	return &Exporter{
		target:        target,
		period:        period,
		streamingTime: streamingTime,
		success:       prometheus.NewDesc(prometheus.BuildFQName(namespace, "", "success"), "Was the last measurement for the probe successful.", nil, nil),
		bitrate:       prometheus.NewDesc(prometheus.BuildFQName(namespace, "", "bitrate"), "Bitrate of the stream in kbit/s.", nil, nil),
		latency:       prometheus.NewDesc(prometheus.BuildFQName(namespace, "", "latency"), "Latency of the target in ms.", nil, nil),
	}
}

// Describe describes all the metrics exported by the Stream Stats Exporter. It
// implements prometheus.Collector.
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- e.success
	ch <- e.bitrate
	ch <- e.latency
}

// Collect measures network stats and delivers them as Prometheus
// metrics. It implements prometheus.Collector.
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	e.mutex.Lock() // To protect metrics from concurrent collects.
	defer e.mutex.Unlock()

	bitrate, latency, err := runAnalysis(e.target, e.streamingTime)
	if err != nil {
		ch <- prometheus.MustNewConstMetric(e.success, prometheus.GaugeValue, 0)
		ssErrors.Inc()
		log.Errorf("Failed to run network analysis: %s", err)
		return
	}

	ch <- prometheus.MustNewConstMetric(e.success, prometheus.GaugeValue, 1)
	ch <- prometheus.MustNewConstMetric(e.bitrate, prometheus.GaugeValue, bitrate)
	ch <- prometheus.MustNewConstMetric(e.latency, prometheus.GaugeValue, latency)
}

func handler(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("target")
	if target == "" {
		http.Error(w, "'target' parameter must be specified", http.StatusBadRequest)
		ssErrors.Inc()
		return
	}

	var runPeriod time.Duration
	var period = r.URL.Query().Get("period")
	if period != "" {
		var err error
		runPeriod, err = time.ParseDuration(period)
		if err != nil {
			http.Error(w, fmt.Sprintf("'period' parameter must be a duration: %s", err), http.StatusBadRequest)
			ssErrors.Inc()
			return
		}
	}
	if runPeriod.Seconds() == 0 {
		runPeriod = time.Second * 5
	}

	var streamingTime int
	period = r.URL.Query().Get("streamingTime")
	if period != "" {
		var err error
		streamingTime, err = strconv.Atoi(period)
		if err != nil {
			http.Error(w, fmt.Sprintf("'streamingTime' parameter must be an integer: %s", err), http.StatusBadRequest)
			ssErrors.Inc()
			return
		}
	}
	if streamingTime == 0 {
		streamingTime = 5
	}

	start := time.Now()
	registry := prometheus.NewRegistry()
	exporter := NewExporter(target, runPeriod, streamingTime)
	registry.MustRegister(exporter)

	// Delegate http serving to Prometheus client library, which will call collector.Collect.
	h := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	h.ServeHTTP(w, r)

	duration := time.Since(start).Seconds()
	ssDuration.Observe(duration)
}

func runAnalysis(streamUrl string, streamingTime int) (float64, float64, error) {
	var bitrate float64
	var latency float64

	var err error
	var wg sync.WaitGroup

	// Measure bitrate in the background
	wg.Add(1)
	go func() {
		bitrate, err = getBitrate(streamUrl, streamingTime, &wg)
	}()

	// Measure latency in the background
	wg.Add(1)
	go func() {
		latency, err = getLatency(streamUrl, &wg)
	}()

	// Wait for the background tasks to complete
	wg.Wait()

	if err != nil {
		return 0, 0, err
	} else {
		return bitrate, latency, nil
	}
}

// Connects to the stream and report its average bitrate.
func getBitrate(videoUrl string, streamingTime int, wg *sync.WaitGroup) (float64, error) {
	defer wg.Done()

	if err := vlc.Init("--no-video", "--quiet"); err != nil {
		return 0, err
	}
	defer vlc.Release()

	// Create a new player.
	player, err := vlc.NewPlayer()
	if err != nil {
		return 0, err
	}
	defer func() {
		player.Stop()
	}()

	media, err := player.LoadMediaFromURL(videoUrl)
	if err != nil {
		return 0, err
	}
	defer media.Release()

	// Start playing the media.
	err = player.Play()
	if err != nil {
		return 0, err
	}

	// Start collecting the metrics.
	var counter = 0
	var bitRates float64 = 0
	for !player.IsPlaying() { // wait for the player to start
	}
	for player.IsPlaying() { // collect bitrate
		stats, err := media.Stats()
		if err != nil {
			return 0, nil
		}
		inputBitrate := stats.DemuxBitRate * 8
		if inputBitrate != 0 {
			bitRates += inputBitrate
			counter++
		}
		if counter == streamingTime {
			player.Stop()
			media.Release()
		}
		time.Sleep(1 * time.Second)
	}
	return bitRates / float64(counter), nil
}

// Parses domain name from the url.
func parseDomainFromUrl(urlAddress string) (string, error) {
	u, err := url.Parse(urlAddress)
	if err != nil {
		return "", err
	}

	return strings.Split(u.Host, ":")[0], nil
}

// Measures latency for the url.
func getLatency(url string, wg *sync.WaitGroup) (float64, error) {
	defer wg.Done()
	var latency float64
	domain, err := parseDomainFromUrl(url)
	if err != nil {
		return 0, err
	}

	p := fastping.NewPinger()
	ra, err := net.ResolveIPAddr("ip4:icmp", domain)
	if err != nil {
		return 0, err
	}
	p.AddIPAddr(ra)
	p.OnRecv = func(addr *net.IPAddr, rtt time.Duration) {
		latency = float64(rtt.Milliseconds())
	}
	err = p.Run()
	if err != nil {
		return 0, err
	}

	return latency, nil
}

func main() {
	log.AddFlags(kingpin.CommandLine)
	kingpin.Version(version.Print("stream_stats_exporter"))
	kingpin.HelpFlag.Short('h')
	kingpin.Parse()

	log.Info("Starting Stream Stats Exporter", version.Info())
	log.Info("Build context", version.BuildContext())

	prometheus.MustRegister(version.NewCollector("stream_stats_exporter"))
	prometheus.MustRegister(ssDuration)
	prometheus.MustRegister(ssErrors)

	http.Handle(*metricsPath, promhttp.Handler())
	http.HandleFunc("/probe", handler)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, err := w.Write([]byte(`<html>
    <head><title>Stream Stats Exporter</title></head>
    <body>
    <h1>Stream Stats Exporter</h1>
    <p><a href='` + *metricsPath + `'>Metrics</a></p>
	<p><a href="/probe">Probe</a></p>
    </html>`))
		if err != nil {
			log.Warnf("Failed to write to HTTP client: %s", err)
		}
	})

	srv := &http.Server{
		Addr:         *listenAddress,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	log.Infof("Listening on %s", srv.Addr)
	log.Fatal(srv.ListenAndServe())
}
