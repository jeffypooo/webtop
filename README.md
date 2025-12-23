# WebTop

A lightweight, real-time system metrics dashboard built with Go, Templ, and HTMX.

## Features

- **Real-time Monitoring**: Live updates for CPU, Memory, Disk, and Network usage via Server-Sent Events (SSE).
- **Process Viewer**: Displays top processes sorted by CPU usage.
- **Interactive Controls**: Adjust refresh interval and process count limit on the fly.
- **Deployment Ready**: Includes Magefile for easy cross-compilation and deployment to Raspberry Pi (Linux ARM64).


## Getting Started

### Prerequisites

- Go 1.25+
- [Mage](https://magefile.org/) (optional, for build commands)
- [Templ](https://templ.guide) CLI (if you need to regenerate templates)

### Running Locally

1. Clone the repository:
   ```bash
   git clone https://github.com/jeffypooo/webtop.git
   cd webtop
   ```

2. Run the server:
   ```bash
   go run cmd/server.go
   ```

3. Open your browser and navigate to `http://localhost:8080`.

## Development

If you modify the `.templ` files, you need to regenerate the Go code:

```bash
templ generate
```

## Deployment (Raspberry Pi)

This project includes a `Magefile` configured for deploying to a Raspberry Pi (Linux ARM64).

1. **Build** the binary:
   ```bash
   mage build
   ```

2. **Deploy** to your Pi:
   ```bash
   # Usage: mage deploy <host> <username>
   mage deploy 192.168.1.100 pi
   ```

3. **Start** the server remotely:
   ```bash
   # Usage: mage start <host> <username>
   mage start 192.168.1.100 pi
   ```
