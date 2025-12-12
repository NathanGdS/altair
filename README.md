# Altair

A distributed asynchronous messaging platform developed in Go. Made only for study purposes.

## 🚀 About the Project

Altair is an asynchronous messaging system that allows sending and receiving messages through topics, with disk persistence and delivery via HTTP push consumers. Inspired by Apache Kafka, but with a simpler and more focused implementation.

## 🛠️ Technologies

- **Go** - Main language (strong in concurrency and performance)
- **Htmx** - Simple Dashboard
- **Append-only Log** - Data persistence
- **Distributed System** - Scalable architecture
- **HTTP Push** - Consumer delivery strategy

## 📋 Features

- 🔄 Message reception in topics
- 🔄 Disk persistence (append-only log)
- 🔄 HTTP push delivery to consumers
- 🔄 Partitioning
- 🔄 Replication and persistence
- 🔄 Offset tracking per consumer
- 🔄 HTTP push delivery
- 🔄 Backpressure

## 🏗️ Architecture

```
┌─────────────┐    ┌─────────────┐    ┌─────────────┐
│   Producer  │───▶│   Altair    │───▶│  Consumer   │
│             │    │   Broker    │    │   (HTTP)    │
└─────────────┘    └─────────────┘    └─────────────┘
                          │
                          ▼
                   ┌─────────────┐
                   │ Append-only │
                   │     Log     │
                   └─────────────┘
```

## 🚀 How to Run

```bash
# Run the project
make run

# Or directly with Go
go run ./main.go
```

## Accessing the Dashboard

Access the following Url:

`http://localhost:8080/`

### Dashboard Features

- Pending messages to be delivered
- Delivered Messages
- Current Purge interval
- Progress bar of delivered messages

![alt text](docs/dashboard-preview.png)

## Performance Test

Running 30s test @ http://localhost:8080/publish
20 connections with 20 pipelining factor

┌─────────┬───────┬───────┬────────┬────────┬──────────┬─────────┬─────────┐
│ Stat    │ 2.5%  │ 50%   │ 97.5%  │ 99%    │ Avg      │ Stdev   │ Max     │
├─────────┼───────┼───────┼────────┼────────┼──────────┼─────────┼─────────┤
│ Latency │ 19 ms │ 28 ms │ 100 ms │ 105 ms │ 38.25 ms │ 40.9 ms │ 1134 ms │
└─────────┴───────┴───────┴────────┴────────┴──────────┴─────────┴─────────┘
┌───────────┬────────┬────────┬─────────┬─────────┬──────────┬──────────┬────────┐
│ Stat      │ 1%     │ 2.5%   │ 50%     │ 97.5%   │ Avg      │ Stdev    │ Min    │
├───────────┼────────┼────────┼─────────┼─────────┼──────────┼──────────┼────────┤
│ Req/Sec   │ 1,224  │ 1,224  │ 13,343  │ 16,119  │ 10,315.5 │ 5,103.26 │ 1,224  │
├───────────┼────────┼────────┼─────────┼─────────┼──────────┼──────────┼────────┤
│ Bytes/Sec │ 474 kB │ 474 kB │ 5.16 MB │ 6.24 MB │ 3.99 MB  │ 1.97 MB  │ 474 kB │
└───────────┴────────┴────────┴─────────┴─────────┴──────────┴──────────┴────────┘

Req/Bytes counts sampled once per second.
# of samples: 30

310k requests in 30.04s, 120 MB read. Ryzen 5600x + RTX 3060

Transaction time, with 5s delay in consumer: 1m07s to process all messages.

## 🎯 Technical Challenges

### Partitioning

- Message distribution across multiple partitions
- Load balancing
- Message ordering by key

### Replication and Persistence

- Synchronization between replicas
- Data durability
- Failure recovery

### Offset Tracking

- Consumer read position control
- Offset persistence
- State recovery

### HTTP Push Delivery

- HTTP POST delivery to consumer endpoints
- Retry mechanisms for failed deliveries
- Consumer acknowledgment handling

### Backpressure

- Data flow control
- Overload prevention
- Throttling mechanisms

## 📁 Project Structure

---

**Altair** - Simplified Asynchronous Messaging 🚀
