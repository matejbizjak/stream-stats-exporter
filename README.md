# Stream Stats Exporter

It connects to the video stream (which you specify on `/target` endpoint), measures the bitrate of the stream and the 
latency of the network and export these metrics on `/metrics` endpoint.

#### Usage
Request:
`localhost:8080/probe?target=https://bitdash-a.akamaihd.net/content/MI201109210084_1/m3u8s/f08e80da-bf1d-4e3d-8899-f0f6155f6efa.m3u8&streamingTime=3`

Response:
```
# HELP monitoring_bitrate Bitrate of the stream.
# TYPE monitoring_bitrate gauge
monitoring_bitrate 2.4431438396374383
# HELP monitoring_latency Latency of the target.
# TYPE monitoring_latency gauge
monitoring_latency 25
# HELP monitoring_success Was the last measurement for the probe successful.
# TYPE monitoring_success gauge
monitoring_success 1
```


#### Docker
Docker image is available on the [Docker Hub](https://hub.docker.com/repository/docker/matejbizjak/stream_stats_exporter).

`docker run -dp 8080:8080 matejbizjak/stream_stats_exporter:1.0`

#### Additional scrape config

```
- job_name: "stream_stats_fog2"
  metrics_path: /probe
  static_configs:
    - targets:
      - https://bitdash-a.akamaihd.net/content/MI201109210084_1/m3u8s/f08e80da-bf1d-4e3d-8899-f0f6155f6efa.m3u8
      - <another url>
  params:
    streamingTime: [3]
  relabel_configs:
      - source_labels: [__address__]
        target_label: __param_target
      - source_labels: [__param_target]
        target_label: instance
      - target_label: __address__
        replacement: 10.244.0.23:8080 # ip of the exporter on the node fog2
      - source_labels: [__scrape_interval__]
        target_label: 20
      - source_labels: [__scrape_timeout__]
        target_label: 30
```

#### Build
If you want to build from source you need to meet the 
[requirements](https://github.com/adrg/libvlc-go#prerequisites) for the `libvlc-go` library.