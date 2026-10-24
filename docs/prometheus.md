# Prometheus and Grafana

`/metrics` serves, for each target, whether its last ping got a reply, the round-trip time, and the packet loss, average and jitter over the "samples to include" set in the top bar. Add it to Prometheus:

```yaml
scrape_configs:
  - job_name: omniping
    static_configs:
      - targets: ["192.168.1.20:8080"]
    # Only when OmniPing has a password; any user name works.
    # basic_auth:
    #   username: prometheus
    #   password: your-omniping-password
```

Then build dashboards on it in Grafana.
