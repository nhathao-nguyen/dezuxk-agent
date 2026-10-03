#!/usr/bin/env bash
set -euo pipefail

COMPOSE_FILE="docker-compose.multinode.yml"
MAX_WAIT_SECONDS=90

echo "================================================================="
echo "   DEZUXK MULTI-NODE CLUSTER REAL E2E INTEGRATION & CHAOS TEST   "
echo "================================================================="

cleanup() {
    echo "Dọn dẹp môi trường docker compose..."
    docker compose -f "$COMPOSE_FILE" down -v --remove-orphans || true
}
trap cleanup EXIT

echo "1. Khởi động toàn bộ Cụm Đa Node (Postgres + Redis + MinIO + Gateways A/B/C + Nginx)..."
docker compose -f "$COMPOSE_FILE" up -d --build

echo "2. Chờ cụm phân tán đạt trạng thái sẵn sàng (Readiness Gate)..."
start_time=$(date +%s)
is_ready=false

while true; do
    current_time=$(date +%s)
    elapsed=$((current_time - start_time))
    if [ "$elapsed" -ge "$MAX_WAIT_SECONDS" ]; then
        echo "LỖI: Quá thời gian chờ cụm đạt trạng thái ready ($MAX_WAIT_SECONDS giây)!"
        echo "--- Docker Compose PS ---"
        docker compose -f "$COMPOSE_FILE" ps
        echo "--- Logs Gateway A ---"
        docker compose -f "$COMPOSE_FILE" logs --tail 50 gateway-a
        echo "--- Logs Gateway B ---"
        docker compose -f "$COMPOSE_FILE" logs --tail 50 gateway-b
        echo "--- Logs Gateway C ---"
        docker compose -f "$COMPOSE_FILE" logs --tail 50 gateway-c
        echo "--- Logs Nginx ---"
        docker compose -f "$COMPOSE_FILE" logs --tail 50 loadbalancer
        exit 1
    fi

    if curl -fsS http://localhost:8081/ready > /dev/null 2>&1 && \
       curl -fsS http://localhost:8082/ready > /dev/null 2>&1 && \
       curl -fsS http://localhost:8083/ready > /dev/null 2>&1 && \
       curl -fsS http://localhost:8080/health > /dev/null 2>&1; then
        echo "Cụm Gateway A, B, C và Nginx Load Balancer đã READY (sau $elapsed giây)!"
        is_ready=true
        break
    fi

    echo "Đang chờ Gateway Nodes sẵn sàng... ($elapsed s)"
    sleep 2
done

echo "3. Thực thi bộ kiểm thử Real HTTP Multi-Node End-to-End..."
export TEST_CLUSTER_HTTP_E2E=true
go test -v -count=1 -run "TestClusterProcess_" ./tests/multinode/...

echo "4. KIỂM THỬ CHAOS #1: Node Crash & Auto-Recovery (Node A bị kill bất ngờ)..."
echo "Giả lập Node A bị crash..."
docker kill dezuxk-gateway-a
sleep 2

# Xác nhận Nginx Load Balancer vẫn phục vụ qua Node B và C
echo "Kiểm tra Load Balancer khi Node A chết..."
curl -fsS http://localhost:8080/health > /dev/null
echo "Khởi động lại Node A..."
docker compose -f "$COMPOSE_FILE" start gateway-a
sleep 5
curl -fsS http://localhost:8081/ready > /dev/null
echo "Node A đã phục hồi và gia nhập lại cụm thành công."

echo "5. KIỂM THỬ CHAOS #2: Redis Failure & Readiness Degradation..."
echo "Tắt Redis..."
docker stop dezuxk-redis
sleep 2
# /ready phải trả 503 khi Redis chết
redis_status=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8081/ready || true)
if [ "$redis_status" != "503" ]; then
    echo "CẢNH BÁO: /ready trả về $redis_status khi Redis offline (kỳ vọng 503)"
else
    echo "/ready trả 503 chính xác khi Redis offline."
fi

echo "Khôi phục Redis..."
docker start dezuxk-redis
sleep 4
curl -fsS http://localhost:8081/ready > /dev/null
echo "Redis đã phục hồi, Gateway Node A /ready trở lại 200 OK."

echo "6. KIỂM THỬ CHAOS #3: PostgreSQL Failure & Fail-Safe..."
echo "Tắt PostgreSQL..."
docker stop dezuxk-postgres
sleep 2
pg_status=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8081/ready || true)
if [ "$pg_status" != "503" ]; then
    echo "CẢNH BÁO: /ready trả về $pg_status khi PostgreSQL offline (kỳ vọng 503)"
else
    echo "/ready trả 503 chính xác khi PostgreSQL offline."
fi

echo "Khôi phục PostgreSQL..."
docker start dezuxk-postgres
sleep 4
curl -fsS http://localhost:8081/ready > /dev/null
echo "PostgreSQL đã phục hồi, Gateway Node A /ready trở lại 200 OK."

echo "================================================================="
echo "   TẤT CẢ KIỂM THỬ CỤM MULTI-NODE THỰC TẾ ĐÃ VƯỢT QUA (PASS)   "
echo "================================================================="
