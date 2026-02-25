#!/bin/bash
# Observability Collector - One-Command Bare-Metal Deployment Script
# This script builds and installs the collector with minimal dependencies

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Configuration
BINARY_NAME="sre-collector"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/sre-collector"
SERVICE_NAME="sre-collector"
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo 'dev')}"
#  Function to build the collector
build_collector() {
    echo -e "${GREEN}[2/6] Building observability collector...${NC}"

    # Build Go binary
    BUILD_FLAGS="-ldflags -X 'main.version=$VERSION' -s -w"
    GOOS=linux GOARCH=amd64 go build $BUILD_FLAGS -o $BINARY_NAME ./cmd/collector

    if [[ ! -f $BINARY_NAME ]]; then
        echo -e "${RED}Build failed${NC}"
        exit 1
    fi

    echo -e "${GREEN}Build complete: $BINARY_NAME${NC}"
}

# Function to build the web UI
build_webui() {
    echo -e "${GREEN}[3/6] Building web UI...${NC}"

    if command -v npm &> /dev/null; then
        cd frontend
        npm install --silent
        npm run build
        cd ..
        echo -e "${GREEN}Web UI built successfully${NC}"
    else
        echo -e "${YELLOW}npm not found. Building simple HTML UI instead...${NC}"
        mkdir -p webui
        create_simple_ui
    fi
}

# Function to create a simple HTML UI if npm is not available
create_simple_ui() {
    cat > webui/index.html << 'EOF'
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Observability Agent</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background: #0d1117; color: #c9d1d9; }
        .container { max-width: 1200px; margin: 0 auto; padding: 2rem; }
        .header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 2rem; padding-bottom: 1rem; border-bottom: 1px solid #30363d; }
        .title { display: flex; align-items: center; gap: 0.75rem; font-size: 1.5rem; font-weight: 600; }
        .status { padding: 0.5rem 1rem; border-radius: 99px; font-size: 0.875rem; display: flex; align-items: center; gap: 0.5rem; }
        .status.connected { background: rgba(63, 185, 80, 0.15); color: #3fb950; }
        .status.disconnected { background: rgba(248, 81, 73, 0.15); color: #f85149; }
        .status-dot { width: 8px; height: 8px; border-radius: 50%; background: currentColor; }
        .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(250px, 1fr)); gap: 1rem; margin-bottom: 2rem; }
        .card { background: #161b22; border: 1px solid #30363d; border-radius: 12px; padding: 1.5rem; }
        .card-title { font-size: 0.875rem; color: #8b949e; margin-bottom: 0.5rem; text-transform: uppercase; }
        .card-value { font-size: 2rem; font-weight: 600; }
        .metric-row { display: flex; justify-content: space-between; padding: 0.5rem 0; border-bottom: 1px solid #21262d; }
        .metric-name { color: #58a6ff; font-family: monospace; }
        .metric-value { font-family: monospace; }
        table { width: 100%; border-collapse: collapse; }
        th, td { text-align: left; padding: 0.75rem; border-bottom: 1px solid #21262d; }
        th { color: #8b949e; font-size: 0.875rem; text-transform: uppercase; }
        @keyframes pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.5; } }
        .connected .status-dot { animation: pulse 2s infinite; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <div class="title">
                <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <path d="M22 12h-4l-3 9L9 3l-3 9H2"/>
                </svg>
                Observability Agent
            </div>
            <div class="status disconnected" id="status">
                <span class="status-dot"></span>
                <span id="status-text">Connecting...</span>
            </div>
        </div>
        <div class="grid">
            <div class="card">
                <div class="card-title">CPU Usage</div>
                <div class="card-value" id="cpu">--%</div>
            </div>
            <div class="card">
                <div class="card-title">Memory Available</div>
                <div class="card-value" id="mem">-- GB</div>
            </div>
            <div class="card">
                <div class="card-title">Load Average (1m)</div>
                <div class="card-value" id="load">--</div>
            </div>
            <div class="card">
                <div class="card-title">Processes</div>
                <div class="card-value" id="procs">--</div>
            </div>
        </div>
        <div class="card">
            <h3 style="margin-bottom: 1rem; color: #8b949e;">All Metrics</h3>
            <table>
                <thead><tr><th>Metric</th><th>Value</th><th>Source</th></tr></thead>
                <tbody id="metrics"></tbody>
            </table>
        </div>
    </div>
    <script>
        const API = '/api/v1';
        async function update() {
            try {
                const [status, metrics] = await Promise.all([
                    fetch(`${API}/status`),
                    fetch(`${API}/metrics`)
                ]);
                if (status.ok && metrics.ok) {
                    document.getElementById('status').className = 'status connected';
                    document.getElementById('status-text').textContent = 'Connected';
                    const data = await metrics.json();
                    updateUI(data);
                }
            } catch (e) {
                document.getElementById('status').className = 'status disconnected';
                document.getElementById('status-text').textContent = 'Disconnected';
            }
        }
        function updateUI(metrics) {
            const cpu = metrics.find(m => m.name === 'system.cpu.usage');
            const mem = metrics.find(m => m.name === 'system.mem.available');
            const load = metrics.find(m => m.name === 'system.load.1m');
            const procs = metrics.find(m => m.name === 'process.count');

            document.getElementById('cpu').textContent = cpu ? cpu.value.toFixed(1) + '%' : '--%';
            document.getElementById('mem').textContent = mem ? (mem.value / 1024/1024/1024).toFixed(1) + ' GB' : '-- GB';
            document.getElementById('load').textContent = load ? load.value.toFixed(2) : '--';
            document.getElementById('procs').textContent = procs ? procs.value : '--';

            const tbody = document.getElementById('metrics');
            tbody.innerHTML = metrics.map(m => `
                <tr>
                    <td class="metric-name">${m.name}</td>
                    <td class="metric-value">${m.value.toFixed(2)}</td>
                    <td style="color:#8b949e">${m.source}</td>
                </tr>
            `).join('');
        }
        setInterval(update, 2000);
        update();
    </script>
</body>
</html>
EOF
    echo -e "${GREEN}Simple UI created${NC}"
}

# Function to install the collector
install_collector() {
    echo -e "${GREEN}[4/6] Installing collector...${NC}"

    # Create user
    if ! id -u $BINARY_NAME &>/dev/null; then
        $USE_SUDO useradd -r -s /bin/false -d /var/lib/$BINARY_NAME $BINARY_NAME 2>/dev/null || \
        $USE_SUDO useradd -r -s /sbin/nologin $BINARY_NAME
    fi

    # Create directories
    $USE_SUDO mkdir -p $INSTALL_DIR
    $USE_SUDO mkdir -p $CONFIG_DIR
    $USE_SUDO mkdir -p /var/lib/$BINARY_NAME
    $USE_SUDO mkdir -p /var/log/$BINARY_NAME

    # Copy binary
    $USE_SUDO cp -f $BINARY_NAME $INSTALL_DIR/
    $USE_SUDO chmod 755 $INSTALL_DIR/$BINARY_NAME

    # Copy config
    $USE_SUDO cp -f config/default.yaml $CONFIG_DIR/config.yaml

    # Copy web UI
    if [[ -d webui ]]; then
        $USE_SUDO mkdir -p /var/lib/$BINARY_NAME/webui
        $USE_SUDO cp -r webui/* /var/lib/$BINARY_NAME/webui/
    fi

    # Set ownership
    $USE_SUDO chown -R $BINARY_NAME:$BINARY_NAME /var/lib/$BINARY_NAME /var/log/$BINARY_NAME

    echo -e "${GREEN}Installation complete${NC}"
}

# Function to create systemd service
create_service() {
    echo -e "${GREEN}[5/6] Creating systemd service...${NC}"

    cat > /tmp/$SERVICE_NAME.service << EOF
[Unit]
Description=Observability Agent
After=network.target

[Service]
Type=simple
User=$BINARY_NAME
Group=$BINARY_NAME
ExecStart=$INSTALL_DIR/$BINARY_NAME --config $CONFIG_DIR/config.yaml
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

# Security hardening
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/$BINARY_NAME /var/log/$BINARY_NAME /proc /sys
CapabilityBoundingSet=CAP_SYS_RESOURCE

[Install]
WantedBy=multi-user.target
EOF

    $USE_SUDO cp /tmp/$SERVICE_NAME.service /etc/systemd/system/
    $USE_SUDO systemctl daemon-reload
    $USE_SUDO systemctl enable $SERVICE_NAME

    echo -e "${GREEN}Service created${NC}"
}

# Function to start the service
start_service() {
    echo -e "${GREEN}[6/6] Starting service...${NC}"

    $USE_SUDO systemctl restart $SERVICE_NAME

    # Wait a moment for startup
    sleep 2

    if $USE_SUDO systemctl is-active --quiet $SERVICE_NAME; then
        echo -e "${GREEN}Service started successfully!${NC}"
        echo ""
        echo -e "${GREEN}Access the web UI at: http://localhost:8080${NC}"
        echo -e "Check status with: ${YELLOW}systemctl status $SERVICE_NAME${NC}"
        echo -e "View logs with: ${YELLOW}journalctl -u $SERVICE_NAME -f${NC}"
    else
        echo -e "${RED}Service failed to start. Check logs:${NC}"
        $USE_SUDO journalctl -u $SERVICE_NAME -n 50
    fi
}

# Function to build Docker image
build_docker() {
    echo -e "${GREEN}Building Docker image...${NC}"

    cat > Dockerfile << EOF
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata bash

RUN addgroup -S $BINARY_NAME && \\
    adduser -S -G $BINARY_NAME -h /var/lib/$BINARY_NAME -s /sbin/nologin -D $BINARY_NAME

RUN mkdir -p /etc/$BINARY_NAME /var/lib/$BINARY_NAME /var/log/$BINARY_NAME

COPY $BINARY_NAME /usr/local/bin/$BINARY_NAME
COPY config/default.yaml /etc/$BINARY_NAME/config.yaml
COPY webui /var/lib/$BINARY_NAME/webui

RUN chown -R $BINARY_NAME:$BINARY_NAME /var/lib/$BINARY_NAME /var/log/$BINARY_NAME

EXPOSE 8080 9090

USER $BINARY_NAME

ENTRYPOINT ["/usr/local/bin/$BINARY_NAME"]
CMD ["--config", "/etc/$BINARY_NAME/config.yaml"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \\
    CMD ["/bin/sh", "-c", "wget -q -O- http://localhost:8080/healthz || exit 1"]
EOF

    docker build -t $SERVICE_NAME:$VERSION .
    docker tag $SERVICE_NAME:$VERSION $SERVICE_NAME:latest

    echo -e "${GREEN}Docker image built: $SERVICE_NAME:$VERSION${NC}"
    echo -e "Run with: ${YELLOW}docker run -d --name $SERVICE_NAME -p 8080:8080 -v /proc:/host/proc:ro -v /sys:/host/sys:ro $SERVICE_NAME${NC}"
}

# Main execution
main() {
    # Parse command line arguments
    DEPLOY_TYPE="${1:-bare}"

    case "$DEPLOY_TYPE" in
        bare|systemd|"")
            install_dependencies
            build_collector
            build_webui
            install_collector
            create_service
            start_service
            ;;
        docker)
            install_dependencies
            build_collector
            build_webui
            build_docker
            ;;
        build-only)
            build_collector
            build_webui
            echo -e "${GREEN}Build complete. Binaries ready in current directory.${NC}"
            ;;
        *)
            echo "Usage: $0 [bare|docker|build-only]"
            echo "  bare      - Install as systemd service (default)"
            echo "  docker    - Build Docker image"
            echo "  build-only - Build binaries only"
            exit 1
            ;;
    esac

    echo ""
    echo -e "${GREEN}======================================${NC}"
    echo -e "${GREEN}  Deployment complete!${NC}"
    echo -e "${GREEN}======================================${NC}"
}

main "$@"
