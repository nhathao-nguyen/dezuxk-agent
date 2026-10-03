#!/usr/bin/env bash
# ==============================================================================
# Dezuxk AI Gateway - Production Pre-flight Verification Script
# ==============================================================================
set -eo pipefail

ENV_FILE=".env.production"
CONFIG_FILE="configs/config.production.yaml"
SKIP_INFRA=false
EXTRA_ARGS=()
POSITIONAL=()

while [ $# -gt 0 ]; do
  case "$1" in
    --skip-infra|-skip-infra)
      SKIP_INFRA=true
      shift
      ;;
    --env|-env)
      ENV_FILE="$2"
      shift 2
      ;;
    --config|-config)
      CONFIG_FILE="$2"
      shift 2
      ;;
    -h|--help)
      echo "Sử dụng:"
      echo "  ./scripts/production-preflight.sh [--skip-infra]"
      echo "  ./scripts/production-preflight.sh [ENV_FILE] [CONFIG_FILE] [--skip-infra]"
      echo "  ./scripts/production-preflight.sh --env [ENV_FILE] --config [CONFIG_FILE] [--skip-infra]"
      exit 0
      ;;
    *)
      if [[ "$1" == -* ]]; then
        EXTRA_ARGS+=("$1")
        shift
      else
        POSITIONAL+=("$1")
        shift
      fi
      ;;
  esac
done

if [ ${#POSITIONAL[@]} -ge 1 ]; then
  ENV_FILE="${POSITIONAL[0]}"
fi
if [ ${#POSITIONAL[@]} -ge 2 ]; then
  CONFIG_FILE="${POSITIONAL[1]}"
fi

echo "======================================================"
echo "    DEZUXK PRODUCTION PRE-FLIGHT VERIFICATION"
echo "======================================================"
echo "Env file:    ${ENV_FILE}"
echo "Config file: ${CONFIG_FILE}"
if [ "${SKIP_INFRA}" = true ]; then
  echo "Mode:        Config-only verification (--skip-infra)"
else
  echo "Mode:        Full verification (Config + Infra ping)"
fi
echo ""

if [ ! -f "${ENV_FILE}" ]; then
  echo "LỖI: Không tìm thấy file môi trường: ${ENV_FILE}"
  echo ""
  echo "Vui lòng copy từ configs/production.env.example:"
  echo "  cp configs/production.env.example ${ENV_FILE}"
  exit 1
fi

if [ ! -f "${CONFIG_FILE}" ]; then
  echo "${CONFIG_FILE} not found."
  echo ""
  echo "Create it with:"
  echo ""
  echo "cp configs/config.production.example.yaml ${CONFIG_FILE}"
  exit 1
fi

# Load variables from env file
set -a
# shellcheck disable=SC1090
source "${ENV_FILE}"
set +a

# Prefer Go-based preflight if go is available
if command -v go >/dev/null 2>&1; then
  GO_ARGS=(-env "${ENV_FILE}" -config "${CONFIG_FILE}")
  if [ "${SKIP_INFRA}" = true ]; then
    GO_ARGS+=(-skip-infra)
  fi
  if [ ${#EXTRA_ARGS[@]} -gt 0 ]; then
    GO_ARGS+=("${EXTRA_ARGS[@]}")
  fi
  go run ./cmd/preflight "${GO_ARGS[@]}"
  exit $?
fi

echo "Go compiler không tìm thấy, thực hiện kiểm tra cơ bản qua Shell..."

FAILED=0

check_item() {
  local name="$1"
  local status="$2"
  local details="$3"

  printf "%-24s" "${name}"
  if [ "${status}" -eq 0 ]; then
    printf "\033[32mPASS\033[0m"
    if [ -n "${details}" ]; then
      printf "  (%s)" "${details}"
    fi
    printf "\n"
  else
    printf "\033[31mFAIL\033[0m (%s)\n" "${details}"
    FAILED=1
  fi
}

# 1. Environment
if [ "${DEZUXK_ENV}" = "production" ]; then
  check_item "Environment" 0 ""
else
  check_item "Environment" 1 "DEZUXK_ENV=${DEZUXK_ENV} (phải là production)"
fi

# 2. Test mode disabled
if [ "${DEZUXK_TEST_MODE}" = "false" ] || [ -z "${DEZUXK_TEST_MODE}" ]; then
  check_item "Test mode disabled" 0 ""
else
  check_item "Test mode disabled" 1 "DEZUXK_TEST_MODE=${DEZUXK_TEST_MODE} (phải là false)"
fi

# 3. API Security
if [ -n "${DEZUXK_API_KEY}" ] && [ "${DEZUXK_API_KEY}" != "CHANGE_ME" ]; then
  check_item "API Security" 0 ""
else
  check_item "API Security" 1 "DEZUXK_API_KEY chưa được thiết lập"
fi

# 4. Vault (Master Key)
if [ -n "${DEZUXK_MASTER_KEY}" ] && [ "${DEZUXK_MASTER_KEY}" != "CHANGE_ME" ]; then
  check_item "Vault" 0 ""
else
  check_item "Vault" 1 "DEZUXK_MASTER_KEY chưa được thiết lập"
fi

# 5. Admin Security
if [ -n "${DEZUXK_ADMIN_PASSWORD}" ] && [ "${DEZUXK_ADMIN_PASSWORD}" != "CHANGE_ME" ] && [ "${DEZUXK_ADMIN_PASSWORD}" != "admin" ] && [ "${DEZUXK_ADMIN_PASSWORD}" != "dezuxk_admin_secret_pass" ] && \
   [ -n "${DEZUXK_ADMIN_SESSION_TOKEN}" ] && [ "${DEZUXK_ADMIN_SESSION_TOKEN}" != "CHANGE_ME" ] && [ "${DEZUXK_ADMIN_SESSION_TOKEN}" != "dezuxk_admin_token" ]; then
  check_item "Admin Security" 0 ""
else
  check_item "Admin Security" 1 "Admin password/token chưa an toàn"
fi

# 6. Cluster & Distributed
if [ "${DEZUXK_CLUSTER_ENABLED}" = "true" ] && [ "${DEZUXK_DISTRIBUTED_ENABLED}" = "true" ]; then
  check_item "Cluster" 0 ""
else
  check_item "Cluster" 1 "DEZUXK_CLUSTER_ENABLED và DEZUXK_DISTRIBUTED_ENABLED phải là true"
fi

# 7. PostgreSQL config
if [ "${SKIP_INFRA}" = true ]; then
  check_item "PostgreSQL" 0 "skipped network ping"
else
  if [ "${DEZUXK_STORAGE_DRIVER}" = "postgres" ] && { [ -n "${DEZUXK_POSTGRES_DSN}" ] || [ -n "${DEZUXK_POSTGRES_HOST}" ]; }; then
    check_item "PostgreSQL" 0 ""
  else
    check_item "PostgreSQL" 1 "Thiếu cấu hình PostgreSQL"
  fi
fi

# 8. Redis config
if [ "${SKIP_INFRA}" = true ]; then
  check_item "Redis" 0 "skipped network ping"
else
  if [ -n "${DEZUXK_REDIS_ADDR}" ]; then
    check_item "Redis" 0 ""
  else
    check_item "Redis" 1 "Thiếu DEZUXK_REDIS_ADDR"
  fi
fi

# 9. S3 config
if [ "${SKIP_INFRA}" = true ]; then
  check_item "S3" 0 "skipped network ping"
else
  if [ "${DEZUXK_MEDIA_DRIVER}" = "s3" ] && [ -n "${DEZUXK_S3_BUCKET}" ]; then
    check_item "S3" 0 ""
  else
    check_item "S3" 1 "Thiếu DEZUXK_S3_BUCKET hoặc media driver không phải s3"
  fi
fi

echo ""
if [ "${FAILED}" -ne 0 ]; then
  echo -e "\033[31mPre-flight verification FAILED! Vui lòng khắc phục các mục lỗi trước khi khởi động.\033[0m"
  exit 1
fi

echo -e "\033[32mPre-flight verification PASSED!\033[0m"
exit 0
