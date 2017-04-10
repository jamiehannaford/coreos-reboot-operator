GOFILES:=$(shell find . -name '*.go' | grep -v -E '(./vendor)')

all: \
	bin/linux/reboot-agent \
	bin/linux/reboot-controller

images: GVERSION=$(shell $(CURDIR)/git-version.sh)
images: bin/linux/reboot-agent bin/linux/reboot-controller
	docker build -f Dockerfile-agent -t jamiehannaford/reboot-agent:$(GVERSION) .
	docker build -f Dockerfile-controller -t jamiehannaford/reboot-controller:$(GVERSION) .

check:
	@find . -name vendor -prune -o -name '*.go' -exec gofmt -s -d {} +
	@go vet $(shell go list ./... | grep -v '/vendor/')
	@go test -v $(shell go list ./... | grep -v '/vendor/')

vendor:
	dep ensure

clean:
	rm -rf bin

bin/%: LDFLAGS=-X github.com/jamiehannaford/coreos-reboot-operator/pkg/common.Version=$(shell $(CURDIR)/git-version.sh)
bin/%: $(GOFILES)
	mkdir -p $(dir $@)
	GOOS=$(word 1, $(subst /, ,$*)) GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -a -installsuffix cgo -o $@ github.com/jamiehannaford/coreos-reboot-operator/pkg/$(notdir $@)

rollout-agent:
	bash ./scripts/rollout.sh reboot-agent

rollout-controller:
	bash ./scripts/rollout.sh reboot-controller
