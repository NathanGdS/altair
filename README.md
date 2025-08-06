# Altair

A distributed asynchronous messaging platform developed in Go. Made only for study purposes.

## 🚀 About the Project

Altair is an asynchronous messaging system that allows sending and receiving messages through topics, with disk persistence and delivery via HTTP push consumers. Inspired by Apache Kafka, but with a simpler and more focused implementation.

## 🛠️ Technologies

- **Go** - Main language (strong in concurrency and performance)
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
