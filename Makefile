IMG ?= ghcr.io/jonathanwiemers/ai-operator:latest
AGENT_IMG ?= ghcr.io/jonathanwiemers/ai-operator-agent:latest
CONTROLLER_GEN ?= $(CURDIR)/bin/controller-gen
CONTROLLER_TOOLS_VERSION ?= v0.23.0
HELM ?= helm
CHART := charts/ai-operator
# Calendar version YEAR.MONTH.N, e.g. 2026.10.1 (see hack/next-version.sh).
VERSION ?= $(shell hack/next-version.sh)

.PHONY: all
all: generate manifests fmt vet test build

$(CONTROLLER_GEN):
	GOBIN=$(CURDIR)/bin go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

.PHONY: generate
generate: $(CONTROLLER_GEN) ## Generate deepcopy code.
	$(CONTROLLER_GEN) object:headerFile=hack/boilerplate.go.txt paths=./api/...

.PHONY: manifests
manifests: $(CONTROLLER_GEN) ## Generate CRDs and RBAC.
	$(CONTROLLER_GEN) rbac:roleName=ai-operator crd paths=./... \
		output:crd:artifacts:config=config/crd/bases output:rbac:artifacts:config=config/rbac
	rm -f $(CHART)/files/crds/*.yaml
	cp config/crd/bases/*.yaml $(CHART)/files/crds/
	cp config/rbac/role.yaml $(CHART)/files/rbac/role.yaml

.PHONY: fmt
fmt:
	gofmt -w api cmd internal

.PHONY: vet
vet:
	go vet ./...

.PHONY: test
test:
	go test -race ./...

.PHONY: build
build:
	go build -o bin/manager ./cmd/operator
	go build -o bin/ai-runner ./cmd/runner

.PHONY: docker-build
docker-build:
	docker build -t $(IMG) .
	docker build -f Dockerfile.agent -t $(AGENT_IMG) .

.PHONY: install
install: manifests ## Install the CRDs.
	kubectl apply -k config/crd

.PHONY: deploy
deploy: manifests ## Deploy CRDs, RBAC and the operator.
	kubectl apply -k config/default

.PHONY: run
run: ## Run the operator against the current kubeconfig.
	go run ./cmd/operator

.PHONY: helm-lint
helm-lint: ## Lint and render the chart.
	$(HELM) lint $(CHART) --strict
	$(HELM) template ai-operator $(CHART) --namespace ai-operator-system --set agent.namespaces={agents} > /dev/null

.PHONY: helm-package
helm-package: ## Package the chart as dist/ai-operator-$(VERSION).tgz.
	$(HELM) package $(CHART) --version $(VERSION) --app-version $(VERSION) -d dist

.PHONY: version
version: ## Print the next release version.
	@echo $(VERSION)
