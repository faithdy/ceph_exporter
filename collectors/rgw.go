package collectors

import (
	"encoding/json"
	"log"
	"os/exec"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const rgwGCTimeFormat = "2006-01-02 15:04:05"
const radosgwAdminPath = "/usr/bin/radosgw-admin"

type rgwTaskGC struct {
	Tag     string `json:"tag"`
	Time    string `json:"time"`
	Objects []struct {
		Pool     string `json:"pool"`
		OID      string `json:"oid"`
		Key      string `json:"ky"`
		Instance string `json:"instance"`
	} `json:"objs"`
}

// Expires returns the timestamp that this task will expire and become active
func (gc rgwTaskGC) ExpiresAt() time.Time {
	tmp := strings.SplitN(gc.Time, ".", 2)

	last, err := time.Parse(rgwGCTimeFormat, tmp[0])
	if err != nil {
		return time.Now()
	}
	return last
}

// rgwGetGCTaskList get the RGW Garbage Collection task list
func rgwGetGCTaskList(config string) ([]byte, error) {
	var (
		out []byte
		err error
	)

	if out, err = exec.Command(radosgwAdminPath, "-c", config, "gc", "list", "--include-all").Output(); err != nil {
		return nil, err
	}

	return out, nil
}

// RGWCollector collects metrics from the RGW service
type RGWCollector struct {
	config string

	// ActiveTasks reports the number of (expired) RGW GC tasks
	ActiveTasks *prometheus.GaugeVec
	// ActiveObjects reports the total number of RGW GC objects contained in active tasks
	ActiveObjects *prometheus.GaugeVec

	// PendingTasks reports the number of RGW GC tasks queued but not yet expired
	PendingTasks *prometheus.GaugeVec
	// PendingObjects reports the total number of RGW GC objects contained in pending tasks
	PendingObjects *prometheus.GaugeVec

	getRGWGCTaskList func(string) ([]byte, error)
}

// NewRGWCollector creates an instance of the RGWCollector and instantiates
// the individual metrics that we can collect from the RGW service
func NewRGWCollector(cluster string, config string) *RGWCollector {
	labels := make(prometheus.Labels)
	labels["cluster"] = cluster
	return &RGWCollector{
		config:           config,
		getRGWGCTaskList: rgwGetGCTaskList,

		ActiveTasks: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace:   cephNamespace,
				Name:        "rgw_gc_active_tasks",
				Help:        "RGW GC active task count",
				ConstLabels: labels,
			},
			[]string{},
		),
		ActiveObjects: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace:   cephNamespace,
				Name:        "rgw_gc_active_objects",
				Help:        "RGW GC active object count",
				ConstLabels: labels,
			},
			[]string{},
		),
		PendingTasks: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace:   cephNamespace,
				Name:        "rgw_gc_pending_tasks",
				Help:        "RGW GC pending task count",
				ConstLabels: labels,
			},
			[]string{},
		),
		PendingObjects: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace:   cephNamespace,
				Name:        "rgw_gc_pending_objects",
				Help:        "RGW GC pending object count",
				ConstLabels: labels,
			},
			[]string{},
		),
	}
}

func (r *RGWCollector) collectorList() []prometheus.Collector {
	return []prometheus.Collector{
		r.ActiveTasks,
		r.ActiveObjects,
		r.PendingTasks,
		r.PendingObjects,
	}
}

func (r *RGWCollector) collect() error {
	data, err := r.getRGWGCTaskList(r.config)
	if err != nil {
		return err
	}

	tasks := make([]rgwTaskGC, 0, 0)
	err = json.Unmarshal(data, &tasks)
	if err != nil {
		return err
	}

	activeTaskCount := int(0)
	activeObjectCount := int(0)
	pendingTaskCount := int(0)
	pendingObjectCount := int(0)

	now := time.Now()
	for _, task := range tasks {
		if now.Sub(task.ExpiresAt()) > 0 {
			// timer expired these are active
			activeTaskCount += 1
			activeObjectCount += len(task.Objects)
		} else {
			pendingTaskCount += 1
			pendingObjectCount += len(task.Objects)
		}
	}

	r.ActiveTasks.WithLabelValues().Set(float64(activeTaskCount))
	r.PendingTasks.WithLabelValues().Set(float64(pendingTaskCount))

	r.ActiveObjects.WithLabelValues().Set(float64(activeObjectCount))
	r.PendingObjects.WithLabelValues().Set(float64(pendingObjectCount))

	return nil
}

// Describe sends the descriptors of each RGWCollector related metrics we have defined
// to the provided prometheus channel.
func (r *RGWCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, metric := range r.collectorList() {
		metric.Describe(ch)
	}
}

// Collect sends all the collected metrics to the provided prometheus channel.
// It requires the caller to handle synchronization.
func (r *RGWCollector) Collect(ch chan<- prometheus.Metric) {
	err := r.collect()
	if err != nil {
		log.Println("Failed to collect RGW GC stats", err)
	}

	for _, metric := range r.collectorList() {
		metric.Collect(ch)
	}
}
