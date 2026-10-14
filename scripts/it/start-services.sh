#!/usr/bin/env bash
# Starts real Redis, MariaDB (MySQL protocol), MongoDB, RabbitMQ and Kafka on
# loopback ports for the probe integration tests, with known credentials.
#
#   scripts/it/start-services.sh [redis|mysql|mongo|rabbit|kafka|all]
#
# Binaries are looked up on PATH or under $NX_IT_OPT (default /opt/nx-test).
# Credentials and ports are exported by scripts/it/env.sh.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=env.sh
source "$here/env.sh"
mkdir -p "$NX_IT_DIR"/{redis,mysql,mongo,rabbit,kafka,certs}
want="${1:-all}"
log() { echo "[it] $*"; }

certs() {
  local d="$NX_IT_DIR/certs"
  [ -f "$d/ca.pem" ] && return
  openssl req -x509 -newkey rsa:2048 -nodes -days 30 -keyout "$d/ca.key" -out "$d/ca.pem" -subj "/CN=nexmesh-it-ca" 2>/dev/null
  openssl req -newkey rsa:2048 -nodes -keyout "$d/server.key" -out "$d/server.csr" -subj "/CN=localhost" 2>/dev/null
  printf "subjectAltName=DNS:localhost,IP:127.0.0.1\n" > "$d/san.cnf"
  openssl x509 -req -in "$d/server.csr" -CA "$d/ca.pem" -CAkey "$d/ca.key" -CAcreateserial -days 30 -out "$d/server.pem" -extfile "$d/san.cnf" 2>/dev/null
  cat "$d/server.key" "$d/server.pem" > "$d/server-combined.pem"
  # an unrelated CA, used to prove untrusted-certificate detection
  openssl req -x509 -newkey rsa:2048 -nodes -days 30 -keyout "$d/other.key" -out "$d/other-ca.pem" -subj "/CN=other-ca" 2>/dev/null
  # an expired certificate signed by our CA
  openssl req -newkey rsa:2048 -nodes -keyout "$d/expired.key" -out "$d/expired.csr" -subj "/CN=localhost" 2>/dev/null
  openssl x509 -req -in "$d/expired.csr" -CA "$d/ca.pem" -CAkey "$d/ca.key" -CAcreateserial -days 1 -out "$d/expired.pem" -extfile "$d/san.cnf" -not_before 20200101000000Z 2>/dev/null || true
}

start_redis() {
  certs
  redis-server --port "$NX_IT_REDIS_PORT" --bind 127.0.0.1 127.0.0.2 --requirepass "$NX_IT_PASS" \
    --tls-port "$NX_IT_REDIS_TLS_PORT" --tls-cert-file "$NX_IT_DIR/certs/server.pem" --tls-key-file "$NX_IT_DIR/certs/server.key" \
    --tls-ca-cert-file "$NX_IT_DIR/certs/ca.pem" --tls-auth-clients no \
    --user limited on ">$NX_IT_PASS" -@all +ping \
    --dir "$NX_IT_DIR/redis" --daemonize yes --logfile "$NX_IT_DIR/redis/redis.log" --pidfile "$NX_IT_DIR/redis/redis.pid" --save ""
  log "redis on :$NX_IT_REDIS_PORT (tls :$NX_IT_REDIS_TLS_PORT)"
}

start_mysql() {
  local d="$NX_IT_DIR/mysql"
  if [ ! -d "$d/data/mysql" ]; then
    mariadb-install-db --user=root --datadir="$d/data" --auth-root-authentication-method=normal >/dev/null 2>&1
  fi
  mariadbd --user=root --datadir="$d/data" --port="$NX_IT_MYSQL_PORT" --bind-address=127.0.0.1 --socket="$d/mysql.sock" \
    --pid-file="$d/mysql.pid" --skip-grant-tables=0 >"$d/mysql.log" 2>&1 &
  for _ in $(seq 1 60); do mariadb --socket="$d/mysql.sock" -uroot -e 'select 1' >/dev/null 2>&1 && break; sleep 0.5; done
  mariadb --socket="$d/mysql.sock" -uroot <<SQL
DROP USER IF EXISTS ''@'localhost';
DROP USER IF EXISTS ''@'$(hostname)';
CREATE DATABASE IF NOT EXISTS orders;
CREATE DATABASE IF NOT EXISTS secret_db;
CREATE USER IF NOT EXISTS 'app'@'%' IDENTIFIED BY '$NX_IT_PASS';
GRANT SELECT ON orders.* TO 'app'@'%';
CREATE USER IF NOT EXISTS 'nodb'@'%' IDENTIFIED BY '$NX_IT_PASS';
FLUSH PRIVILEGES;
SQL
  log "mariadb on :$NX_IT_MYSQL_PORT"
}

start_mongo() {
  certs
  local d="$NX_IT_DIR/mongo" bin
  bin="$(command -v mongod || ls "$NX_IT_OPT"/mongodb-linux-*/bin/mongod)"
  # --auth with an empty database enables the localhost exception: the Go
  # integration test creates the users on first run (internal/probe/it_test.go).
  "$bin" --dbpath "$d" --port "$NX_IT_MONGO_PORT" --bind_ip 127.0.0.1 --auth --fork \
    --logpath "$d/mongod.log" --pidfilepath "$d/mongod.pid" >/dev/null
  log "mongod (auth) on :$NX_IT_MONGO_PORT"
}

start_rabbit() {
  local d="$NX_IT_DIR/rabbit"
  export RABBITMQ_MNESIA_BASE="$d/mnesia" RABBITMQ_LOG_BASE="$d/log" RABBITMQ_NODE_PORT="$NX_IT_AMQP_PORT" \
         RABBITMQ_NODENAME="nxit@localhost" RABBITMQ_NODE_IP_ADDRESS=127.0.0.1 RABBITMQ_SERVER_START_ARGS="-rabbitmq_management listener [{port,$NX_IT_AMQP_MGMT_PORT}]" \
         RABBITMQ_DIST_PORT=$((NX_IT_AMQP_PORT + 20000)) HOME="$d"
  mkdir -p "$d/mnesia" "$d/log"
  # When run as root the rabbitmq-server wrapper drops to the rabbitmq user.
  if [ "$(id -u)" = 0 ] && id rabbitmq >/dev/null 2>&1; then chown -R rabbitmq: "$d"; fi
  setsid nohup rabbitmq-server >"$d/rabbit.out" 2>&1 </dev/null &
  for _ in $(seq 1 90); do rabbitmqctl -n nxit@localhost status >/dev/null 2>&1 && break; sleep 1; done
  # The Management API (inspectors) listens on $NX_IT_AMQP_MGMT_PORT.
  rabbitmq-plugins -n nxit@localhost enable rabbitmq_management >/dev/null 2>&1 || true
  rabbitmqctl -n nxit@localhost add_user app "$NX_IT_PASS" >/dev/null 2>&1 || true
  rabbitmqctl -n nxit@localhost add_vhost orders >/dev/null 2>&1 || true
  rabbitmqctl -n nxit@localhost set_permissions -p orders app '.*' '.*' '.*' >/dev/null 2>&1 || true
  rabbitmqctl -n nxit@localhost add_user noperm "$NX_IT_PASS" >/dev/null 2>&1 || true
  log "rabbitmq on :$NX_IT_AMQP_PORT"
}

start_kafka() {
  local d="$NX_IT_DIR/kafka" k
  k="$(ls -d "$NX_IT_OPT"/kafka_*/ | head -1)"
  cat > "$d/server.properties" <<CFG
process.roles=broker,controller
node.id=1
controller.quorum.voters=1@127.0.0.1:19093
listeners=PLAINTEXT://127.0.0.1:$NX_IT_KAFKA_PORT,CONTROLLER://127.0.0.1:19093
advertised.listeners=PLAINTEXT://127.0.0.1:$NX_IT_KAFKA_PORT
controller.listener.names=CONTROLLER
listener.security.protocol.map=CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT
log.dirs=$d/data
offsets.topic.replication.factor=1
transaction.state.log.replication.factor=1
transaction.state.log.min.isr=1
group.initial.rebalance.delay.ms=0
CFG
  if [ ! -d "$d/data" ]; then
    "$k/bin/kafka-storage.sh" format -t "$(JAVA_TOOL_OPTIONS= "$k/bin/kafka-storage.sh" random-uuid | tail -1)" -c "$d/server.properties" >/dev/null
  fi
  KAFKA_HEAP_OPTS="-Xmx256m -Xms128m" JAVA_TOOL_OPTIONS= nohup "$k/bin/kafka-server-start.sh" "$d/server.properties" >"$d/kafka.log" 2>&1 &
  log "kafka on :$NX_IT_KAFKA_PORT"
}

case "$want" in
  redis) start_redis ;; mysql) start_mysql ;; mongo) start_mongo ;; rabbit) start_rabbit ;; kafka) start_kafka ;;
  all) start_redis; start_mysql; start_mongo; start_rabbit; start_kafka ;;
  *) echo "unknown service $want"; exit 2 ;;
esac
