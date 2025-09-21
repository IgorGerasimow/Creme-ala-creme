# Variables
COMPOSE ?= docker compose
KUBECTL ?= kubectl

# Auto-detect app directory: use current directory name by default
HELLO_DIR ?= $(notdir $(CURDIR))

# Resolve the directory containing deploy/local manifests
APP_DIR := $(HELLO_DIR)
ifneq ($(wildcard $(APP_DIR)/deploy/local),) #simple check if directory exists
  # APP_DIR is good
else
  ifneq ($(wildcard hello-world/deploy/local),) #if not, check if hello-world directory exists
    APP_DIR := hello-world
  endif
endif

LOCAL_NS := $(APP_DIR)/deploy/local/namespace.yaml
LOCAL_DEPLOY := $(APP_DIR)/deploy/local/deployment.yaml

PROM_CONFIG := prometheus/prometheus.yml
ALERT_CONFIG := alertmanager/alertmanager.yml
PROM_IMAGE := prom/prometheus:v2.54.1
ALERT_IMAGE := prom/alertmanager:v0.26.0

.DEFAULT_GOAL := help

.PHONY: help
help:
	@echo "Available targets:"
	@echo "  compose-up            - Build and start all services via docker-compose"
	@echo "  compose-down          - Stop and remove docker-compose stack"
	@echo "  compose-logs          - Tail logs from docker-compose services"
	@echo "  test                  - Run Go tests (auto-detected app dir)"
	@echo "  build-local           - Build $(APP_DIR):local Docker image"
	@echo "  deploy-local          - Build image and kubectl apply local manifests"
	@echo "  delete-local          - kubectl delete local manifests"
	@echo "  monitor-up            - Start Alertmanager and Prometheus via docker-compose"
	@echo "  monitor-down          - Stop Alertmanager and Prometheus"
	@echo "  monitor-logs          - Tail monitoring service logs"
	@echo "  monitor-check         - Validate Prometheus and Alertmanager configs"

.PHONY: compose-up
compose-up:
	$(COMPOSE) up -d --build

.PHONY: compose-down
compose-down:
	$(COMPOSE) down

.PHONY: compose-logs
compose-logs:
	$(COMPOSE) logs -f

.PHONY: test
test:
	cd $(APP_DIR) && go test ./...

.PHONY: build-local
build-local:
	docker build -t $(APP_DIR):local $(APP_DIR)

.PHONY: deploy-local
deploy-local: build-local
	$(KUBECTL) apply -f $(LOCAL_NS)
	$(KUBECTL) apply -f $(LOCAL_DEPLOY)

.PHONY: delete-local
delete-local:
	-$(KUBECTL) delete -f $(LOCAL_DEPLOY)
	-$(KUBECTL) delete -f $(LOCAL_NS)

.PHONY: monitor-up
monitor-up:
	$(COMPOSE) up -d alertmanager prometheus

.PHONY: monitor-down
monitor-down:
	$(COMPOSE) stop alertmanager prometheus

.PHONY: monitor-logs
monitor-logs:
	$(COMPOSE) logs -f alertmanager prometheus

.PHONY: monitor-check
monitor-check:
	docker run --rm \
		-v $(PWD)/$(PROM_CONFIG):/etc/prometheus/prometheus.yml:ro \
		-v $(PWD)/$(APP_DIR)/monitoring/prometheus-rules.yaml:/etc/prometheus/rules/hello-world-rules.yaml:ro \
		$(PROM_IMAGE) promtool check config /etc/prometheus/prometheus.yml
	docker run --rm \
		-v $(PWD)/$(ALERT_CONFIG):/etc/alertmanager/alertmanager.yml:ro \
		$(ALERT_IMAGE) amtool check-config /etc/alertmanager/alertmanager.yml
