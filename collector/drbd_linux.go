// Copyright 2016 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package collector

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"
)

// Numerical metric provided by /proc/drbd.
type drbdNumericalMetric struct {
	desc       *prometheus.Desc
	valueType  prometheus.ValueType
	multiplier float64
}

func newDrbdNumericalMetric(name string, desc string, valueType prometheus.ValueType, multiplier float64) drbdNumericalMetric {
	return drbdNumericalMetric{
		desc: prometheus.NewDesc(
			prometheus.BuildFQName(Namespace, "drbd", name),
			desc,
			[]string{"device"}, nil),
		valueType:  valueType,
		multiplier: multiplier,
	}
}

// String pair metric provided by /proc/drbd.
type drbdStringPairMetric struct {
	desc      *prometheus.Desc
	valueOkay string
}

func (metric *drbdStringPairMetric) isOkay(value string) float64 {
	if value == metric.valueOkay {
		return 1
	}
	return 0
}

func newDrbdStringPairMetric(name string, desc string, valueOkay string) drbdStringPairMetric {
	return drbdStringPairMetric{
		desc: prometheus.NewDesc(
			prometheus.BuildFQName(Namespace, "drbd", name),
			desc,
			[]string{"device", "node"}, nil),
		valueOkay: valueOkay,
	}
}

var (
	drbdNumericalMetrics = map[string]drbdNumericalMetric{
		"ns": newDrbdNumericalMetric(
			"network_sent_bytes",
			"Volume of net data sent to the partner via the network connection.",
			prometheus.CounterValue,
			1024),
		"nr": newDrbdNumericalMetric(
			"network_received_bytes",
			"Volume of net data received by the partner via the network connection.",
			prometheus.CounterValue,
			1),
		"dw": newDrbdNumericalMetric(
			"disk_written_bytes",
			"Net data written on local hard disk.",
			prometheus.CounterValue,
			1024),
		"dr": newDrbdNumericalMetric(
			"disk_read_bytes",
			"Net data read from local hard disk.",
			prometheus.CounterValue,
			1024),
		"al": newDrbdNumericalMetric(
			"activitylog_writes",
			"Number of updates of the activity log area of the meta data.",
			prometheus.CounterValue,
			1),
		"bm": newDrbdNumericalMetric(
			"bitmap_writes",
			"Number of updates of the bitmap area of the meta data.",
			prometheus.CounterValue,
			1),
		"lo": newDrbdNumericalMetric(
			"local_pending",
			"Number of open requests to the local I/O sub-system.",
			prometheus.GaugeValue,
			1),
		"pe": newDrbdNumericalMetric(
			"remote_pending",
			"Number of requests sent to the partner, but that have not yet been answered by the latter.",
			prometheus.GaugeValue,
			1),
		"ua": newDrbdNumericalMetric(
			"remote_unacknowledged",
			"Number of requests received by the partner via the network connection, but that have not yet been answered.",
			prometheus.GaugeValue,
			1),
		"ap": newDrbdNumericalMetric(
			"application_pending",
			"Number of block I/O requests forwarded to DRBD, but not yet answered by DRBD.",
			prometheus.GaugeValue,
			1),
		"ep": newDrbdNumericalMetric(
			"epochs",
			"Number of Epochs currently on the fly.",
			prometheus.GaugeValue,
			1),
		"oos": newDrbdNumericalMetric(
			"out_of_sync_bytes",
			"Amount of data known to be out of sync.",
			prometheus.GaugeValue,
			1024),
	}
	drbdStringPairMetrics = map[string]drbdStringPairMetric{
		"ro": newDrbdStringPairMetric(
			"node_role_is_primary",
			"Whether the role of the node is in the primary state.",
			"Primary"),
		"ds": newDrbdStringPairMetric(
			"disk_state_is_up_to_date",
			"Whether the disk of the node is up to date.",
			"UpToDate"),
	}

	drbdConnected = prometheus.NewDesc(
		prometheus.BuildFQName(Namespace, "drbd", "connected"),
		"Whether DRBD is connected to the partner.",
		[]string{"device"}, nil)
)

type drbdCollector struct{}

func init() {
	Factories["drbd"] = NewDrbdCollector
}

func NewDrbdCollector() (Collector, error) {
	return &drbdCollector{}, nil
}

func (c *drbdCollector) Update(ch chan<- prometheus.Metric) (err error) {
	statsFile := procFilePath("drbd")
	file, err := os.Open(statsFile)
	if err != nil {
		if os.IsNotExist(err) {
			log.Debugf("Not collecting DRBD statistics, as %s does not exist: %s", statsFile)
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Split(bufio.ScanWords)
	device := "unknown"
	for scanner.Scan() {
		field := scanner.Text()
		if kv := strings.Split(field, ":"); len(kv) == 2 {
			if id, err := strconv.ParseUint(kv[0], 10, 64); err == nil && kv[1] == "" {
				device = fmt.Sprintf("drbd%d", id)
			} else if metric, ok := drbdNumericalMetrics[kv[0]]; ok {
				// Numerical value.
				value, err := strconv.ParseFloat(kv[1], 64)
				if err != nil {
					return err
				}
				ch <- prometheus.MustNewConstMetric(
					metric.desc, metric.valueType,
					value*metric.multiplier, device)
			} else if metric, ok := drbdStringPairMetrics[kv[0]]; ok {
				// String pair value.
				values := strings.Split(kv[1], "/")
				ch <- prometheus.MustNewConstMetric(
					metric.desc, prometheus.GaugeValue,
					metric.isOkay(values[0]), device, "local")
				ch <- prometheus.MustNewConstMetric(
					metric.desc, prometheus.GaugeValue,
					metric.isOkay(values[1]), device, "remote")
			} else if kv[0] == "cs" {
				// Connection state.
				var connected float64 = 0
				if kv[1] == "Connected" {
					connected = 1
				}
				ch <- prometheus.MustNewConstMetric(
					drbdConnected, prometheus.GaugeValue,
					connected, device)
			} else {
				log.Infof("Don't know how to process key-value pair [%s: %s]", kv[0], kv[1])
			}
		} else {
			log.Infof("Don't know how to process string %s", field)
		}
	}
	return scanner.Err()
}
