package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

func setupSocket(socketPath string) (net.Listener, error) {
	os.RemoveAll(filepath.Dir(socketPath))
	if err := os.MkdirAll(filepath.Dir(socketPath), 0700); err != nil {
		return nil, fmt.Errorf("failed to create directory %q: %v", filepath.Dir(socketPath), err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on %q: %v", socketPath, err)
	}

	log.Printf("Listening on: unix://%s", socketPath)
	return listener, nil
}

func setupSignals(socketPath string) {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-interrupt
		os.RemoveAll(filepath.Dir(socketPath))
		os.Exit(0)
	}()
}

// Iops struct
type Iops struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric struct {
				Name              string `json:"__name__"`
				Instance          string `json:"instance"`
				Job               string `json:"job"`
				KubernetesPodName string `json:"kubernetes_pod_name"`
				OpenebsPv         string `json:"openebs_pv"`
			} `json:"metric"`
			Value []interface{} `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

func getValue(body []byte) (*Iops, error) {
	var s = new(Iops)
	err := json.Unmarshal(body, &s)
	log.Printf("================%+v", s)
	if err != nil {
		fmt.Println("whoops:")
	}
	return s, err
}
func main() {
	//we put the in a sub-directory to have more control on the permissions
	const socketPath = "/var/run/scope/plugins/iowait/iowait.sock"
	//url := "http://cortex-agent-service.maya-system.svc.cluster.local:80/api/v1/query?query=OpenEBS_write_iops"
	//url := "http://172.17.0.5:80/api/v1/query?query=OpenEBS_write_iops"
    url := os.Getenv("CORTEXAGENT")
	fmt.Printf("len-1---%+v\n", len(url))
	fmt.Printf("len-2---%+v\n", len(strings.TrimSpace(url)))

	fmt.Printf("---%+v\n", url)
	fmt.Printf("---%+v\n", strings.TrimSpace(url))


	out, err := exec.Command("curl", strings.TrimSpace(url)).CombinedOutput()
	fmt.Println("output:", string(out), err)

	// Get request to url
	res, err := http.Get(strings.TrimSpace(url))
	if err != nil {
		log.Println(err.Error())
		return
	}

	body, err := ioutil.ReadAll(res.Body)
	// log.Printf("Body %+v", body)
	if err != nil {
		log.Println(err.Error())
	}

	s, err := getValue([]byte(body))

	log.Printf("======%+v", s)
	//fmt.Println("=====================================pV ID", s.Data.Result[0].Metric.OpenebsPv)
	// Handle the exit signal
	setupSignals(socketPath)

	// log.Printf("Starting on %s...\n", pvID)

	listener, err := setupSocket(socketPath)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		listener.Close()
		os.RemoveAll(filepath.Dir(socketPath))
	}()
    log.Printf("pr: %v",s)
	plugin, err := NewPlugin(s.Data.Result[0].Metric.OpenebsPv)
	if err != nil {
		log.Fatalf("Failed to create a plugin: %v", err)
	}
	//logrus.Infof("=======>> %+v", plugin)
	http.HandleFunc("/report", plugin.Report)
	if err := http.Serve(listener, nil); err != nil {
		log.Printf("error: %v", err)
	}
}

// NewPlugin instantiates a new plugin
func NewPlugin(pv string) (*Plugin, error) {
	pvID := pv
	plugin := &Plugin{
		PersistentVolumeID: pvID,
	}
	return plugin, nil
}

// Plugin groups the methods a plugin needs
type Plugin struct {
	pvid               string
	PersistentVolumeID string

	lock       sync.Mutex
	iowaitMode bool
}

type request struct {
	NodeID string
}

type response struct {
	ShortcutReport *report `json:"shortcutReport,omitempty"`
}

type report struct {
	PersistentVolume topology
	Plugins          []pluginSpec
}

type topology struct {
	Nodes           map[string]node           `json:"nodes"`
	MetricTemplates map[string]metricTemplate `json:"metric_templates"`
}

type node struct {
	Metrics map[string]metric `json:"metrics"`
}

type metric struct {
	Samples []sample `json:"samples,omitempty"`
	Min     float64  `json:"min"`
	Max     float64  `json:"max"`
}

type sample struct {
	Date  time.Time `json:"date"`
	Value float64   `json:"value"`
}

type metricTemplate struct {
	ID       string  `json:"id"`
	Label    string  `json:"label,omitempty"`
	Format   string  `json:"format,omitempty"`
	Priority float64 `json:"priority,omitempty"`
}

type pluginSpec struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Interfaces  []string `json:"interfaces"`
	APIVersion  string   `json:"api_version,omitempty"`
}

func (p *Plugin) makeReport() (*report, error) {
	metrics, err := p.metrics()
	if err != nil {
		return nil, err
	}
	rpt := &report{
		PersistentVolume: topology{
			Nodes: map[string]node{
				p.getTopologyPv(): {
					Metrics: metrics,
				},
			},
			MetricTemplates: p.metricTemplates(),
		},
		Plugins: []pluginSpec{
			{
				ID:          "iowait",
				Label:       "iowait",
				Description: "Adds a graph of CPU IO Wait to Pod",
				Interfaces:  []string{"reporter"},
				APIVersion:  "1",
			},
		},
	}
	return rpt, nil
}

func (p *Plugin) metrics() (map[string]metric, error) {
	value := 33.66
	id, _ := p.metricIDAndName()
	metrics := map[string]metric{
		id: {
			Samples: []sample{
				{
					Date:  time.Now(),
					Value: value,
				},
			},
			Min: 0,
			Max: 100,
		},
	}
	return metrics, nil
}

func (p *Plugin) metricTemplates() map[string]metricTemplate {
	id, name := p.metricIDAndName()
	return map[string]metricTemplate{
		id: {
			ID:       id,
			Label:    name,
			Format:   "percent",
			Priority: 0.1,
		},
	}
}

// Report is called by scope when a new report is needed. It is part of the
// "reporter" interface, which all plugins must implement.
func (p *Plugin) Report(w http.ResponseWriter, r *http.Request) {
	p.lock.Lock()
	defer p.lock.Unlock()
	log.Println(r.URL.String())
	rpt, err := p.makeReport()
	if err != nil {
		log.Printf("error: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	raw, err := json.Marshal(*rpt)
	if err != nil {
		log.Printf("error: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(raw)
}

func (p *Plugin) getTopologyPv() string {
	return fmt.Sprintf("%s;<persistent_volume>", p.PersistentVolumeID)
}

func (p *Plugin) metricIDAndName() (string, string) {
	return "iops", "Iops"
}
