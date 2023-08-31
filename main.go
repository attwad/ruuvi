// Package main contains a simple go server that periodically fetches data from a Ruuvi tag and publishes it as prometheus metrics for monitoring.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"tinygo.org/x/bluetooth"
)

var (
	adapter         = bluetooth.DefaultAdapter
	numMeasurements = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "measurement_count",
		},
	)
	numMeasurementsErrs = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "measurement_err_count",
		},
	)
	tempGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "temperature",
		Help: "Temperature in celcius",
	})
	humidityGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "humidity",
		Help: "Humidity in percentage",
	})
	pressureGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "pressure",
		Help: "Atmospheric pressure in hectopascal",
	})
	measureTime = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "measurement_duration",
		Help:    "Seconds it took to make a measurement",
		Buckets: prometheus.LinearBuckets(1, 5, 20),
	})

	measureEvery = flag.Duration("measure_every", 5*time.Minute, "Get measurements once every specified duration")
	addr         = flag.String("addr", "127.0.0.1:8045", "address:port to listen on")
)

func parsePacket(buf []byte) error {
	fmt.Printf("data (len: %d): %v (%x)\n", len(buf), buf, buf)
	// Notifications are like Data format 5, without the mac address because payloads are limited to 20 bytes.
	// https://docs.ruuvi.com/communication/bluetooth-advertisements/data-format-5-rawv2
	// Format is described in:
	// https://github.com/ruuvi/ruuvi-sensor-protocols/blob/master/broadcast_formats.md
	if buf[0] != 5 {
		return fmt.Errorf("invalid format, packet did not start with 5")
	}
	// Temperature
	ts := fmt.Sprintf("%x", buf[1:3])
	t, err := strconv.ParseInt(ts, 16, 64)
	if err != nil {
		return fmt.Errorf("could not convert %s from hexadecimal to decimal: %w", ts, err)
	}
	temp := float64(t) * 0.005 // degrees
	fmt.Printf("Temperature: %.2f°C\n", temp)
	tempGauge.Set(temp)

	// Humidity
	hs := fmt.Sprintf("%x", buf[3:5])
	h, err := strconv.ParseInt(hs, 16, 64)
	if err != nil {
		return fmt.Errorf("could not convert %s from hexadecimal to decimal: %w", hs, err)
	}
	humidity := float64(h) * 0.0025 // percentage
	fmt.Printf("Humidity: %.2f%%\n", humidity)
	humidityGauge.Set(humidity)

	// Pressure
	ps := fmt.Sprintf("%x", buf[5:7])
	p, err := strconv.ParseInt(ps, 16, 64)
	if err != nil {
		return fmt.Errorf("could not convert %s from hexadecimal to decimal: %w", ps, err)
	}
	pressure := (float64(p) + 50000) / 100 // compensate the 50000 offset, in Pa
	fmt.Printf("Pressure: %.2f hPa\n", pressure)
	pressureGauge.Set(pressure)

	// Battery voltage
	// TODO: Fix computation, unclear in which sense the "first 11 bits" are taken...
	// bs := fmt.Sprintf("%x", buf[13:15])
	// fmt.Println(bs)
	// b, err := strconv.ParseInt(bs, 16, 64)
	// if err != nil {
	// 	fmt.Printf("Could not convert %s from hexadecimal to decimal: %v\n", bs, err)
	// 	return
	// }
	// fmt.Println(b)
	// fmt.Println(b & 0x0EFF)
	// batteryVoltage := 1.6 + float32(b&0x0EFF)/1000 // mV above 1.6V
	// fmt.Printf("Battery: %.2fV\n", batteryVoltage)
	return nil
}

func measure() error {
	start := time.Now()
	defer func() {
		measureTime.Observe(time.Since(start).Seconds())
	}()

	// var ruuvi bluetooth.ScanResult
	var stopScanErr error
	buf := make([]byte, 32)

	if err := adapter.Scan(func(adapter *bluetooth.Adapter, device bluetooth.ScanResult) {
		println("found device:", device.Address.String(), device.RSSI, device.LocalName(), device.ManufacturerData(), device.AdvertisementPayload)
		if !strings.Contains(device.LocalName(), "Ruuvi") {
			return
		}

		md := device.ManufacturerData()
		buffer, ok := md[1177]
		if !ok {
			return
		}
		copy(buf, buffer)

		fmt.Println("Stopping scan")
		if err := adapter.StopScan(); err != nil {
			stopScanErr = fmt.Errorf("stopping scan: %w", err)
		}
	}); err != nil {
		return fmt.Errorf("scanning: %w", err)
	}
	if stopScanErr != nil {
		return stopScanErr
	}
	fmt.Println("Stopped scan")

	if err := parsePacket(buf); err != nil {
		return fmt.Errorf("parsing packet: %w", err)
	}
	return nil
}

func main() {
	flag.Parse()
	// Enable BLE interface.
	if err := adapter.Enable(); err != nil {
		log.Fatal(err)
	}

	// Register prometheus metrics
	prometheus.MustRegister(numMeasurements, numMeasurementsErrs, tempGauge, humidityGauge, pressureGauge, measureTime)

	// Register HTTP Server and handlers for prometheus metrics.
	http.Handle("/metrics", promhttp.Handler())
	go http.ListenAndServe(*addr, nil)

	// Do an initial measurement.
	if err := measure(); err != nil {
		log.Fatal(err)
	}
	// Then continue measuring periodically.
	ticker := time.NewTicker(*measureEvery)
	fmt.Println("Starting measurements ticker")
	for range ticker.C {
		if err := measure(); err != nil {
			fmt.Println(err)
		}
	}
}
