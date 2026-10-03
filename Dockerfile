# Multi-stage build cho Dezuxk AI Gateway
# Stage 1: Build binary với CGO (SQLite3)
FROM golang:alpine AS builder

# Cài đặt build toolchain cho CGO (gcc, musl-dev)
RUN apk add --no-cache gcc musl-dev git

WORKDIR /app

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Sao chép mã nguồn và compile binary tối ưu
COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o dezuxk main.go

# Stage 2: Runtime image gọn nhẹ với Chromium headless
FROM alpine:3.20

# Cài đặt thư viện runtime, ca-certificates, Chromium & font chữ tiếng Việt/quốc tế
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    chromium \
    font-noto \
    wget

WORKDIR /app

# Tạo non-root user và group bảo mật
RUN addgroup -S -g 10001 dezuxk && \
    adduser -S -u 10001 -G dezuxk -h /app -s /sbin/nologin dezuxk

# Sao chép binary từ builder stage
COPY --from=builder /app/dezuxk /app/dezuxk
COPY configs/ /app/configs/
RUN if [ ! -f /app/configs/config.yaml ] && [ -f /app/configs/config.example.yaml ]; then \
        cp /app/configs/config.example.yaml /app/configs/config.yaml; \
    fi

# Tạo các thư mục lưu trữ bền vững và phân quyền cho user non-root
RUN mkdir -p /app/storage /app/profiles /app/workspaces && \
    chown -R dezuxk:dezuxk /app

# Chuyển sang user non-root
USER dezuxk:dezuxk

# Cấu hình biến môi trường mặc định
ENV DEZUXK_HOST=0.0.0.0 \
    DEZUXK_PORT=8080 \
    DEZUXK_CHROME_BINARY=/usr/bin/chromium \
    TZ=Asia/Ho_Chi_Minh

# Expose HTTP port
EXPOSE 8080

# Khai báo volume mount cho dữ liệu cấu hình, database và profile
VOLUME ["/app/configs", "/app/storage", "/app/profiles", "/app/workspaces"]

# Chạy Dezuxk Gateway
ENTRYPOINT ["/app/dezuxk"]
CMD ["-config", "configs/config.yaml"]
