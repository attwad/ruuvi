A simple Ruuvi beacon scanner that fetches temperature, pression and humidity periodically and exports those metrics with prometheus.

Running on a raspberry pi at home that also runs the prometheus server and grafana locally.

Build to ship to raspberry pi with: `env GOOS=linux GOARCH=arm64 go build -o ruuvi_arm64`

Run locally with: `go run . --measure_every=15s`