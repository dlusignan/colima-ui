// Package sampler is the Metrics Sampler: on a fixed interval it reads usage
// for every running Docker profile, appends the samples to the history store
// and publishes the latest values to the event hub.
//
// The sampler records usage only. Allocated figures (cpus, memory, disk) are
// not stored and not published; the capacity view takes them from the profile
// itself.
//
// Definitions, one per metric:
//
//  1. VM cpu is the sum of the running containers' CPU percent (100 is one
//     full core). VM memory is the sum of their memory bytes (usage minus
//     inactive page cache). Both leave out dockerd and the guest kernel, so
//     the UI should call them container CPU and container memory.
//  2. VM disk is the Docker engine's layers, containers, volumes and build
//     cache bytes. It is read every 12th tick, asynchronously and single
//     flight per profile, and the last good value is reused in between. A
//     value older than three minutes becomes NULL.
//  3. A container row carries cpu and memory only. A container is recorded
//     from its second sighting, because the first reading has no CPU baseline.
//
// Unknown is never recorded as zero. A stopped profile, or a running profile
// whose runtime is not docker, is skipped without rows, events or per tick
// log lines. A failing source is logged once per outage and once on
// recovery, and the other sources still record.
//
// Shutdown order: Stop the sampler before closing the store. Stopping the
// sampler before the hub only affects the log.
package sampler
