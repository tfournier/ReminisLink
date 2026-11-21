# ReminisLink - pak and protocol
#
# The pak targets come from github.com/tfournier/nextui-cheat-manager. The
# ARM64 build needs the cross toolchain of scripts/setup-toolchain.sh; on any
# machine with Docker, prefix a target with "docker-" to run it inside the
# build image, e.g.:  make docker-release
#
# Main targets:
#   make test            run Go unit tests (no toolchain, CGO_ENABLED=0)
#   make lint            gofmt + go vet
#   make fuzz            run each fuzz target of the patch parsers briefly (FUZZTIME, default 10s)
#   make contract        validate protocol/openapi.yaml (Redocly in Docker) and the test vectors
#   make vectors         regenerate protocol/test-vectors/ and copy them to the Swift tests
#   make headless        run the pak (server + Bonjour, no UI) on a temporary copy of testdata/sdcard
#   make desktop         run the pak with its UI in a window on a temporary copy of testdata/sdcard (SDL2 needed)
#   make screenshots     regenerate docs/screenshots/ with the desktop UI under Xvfb (Docker)
#   make build           cross-compile the ARM64 binary (one binary for every NextUI platform)
#   make pak             assemble the pak folder in build/pak
#   make release         build the release zip in dist/ (Pak Store format, every platform of pak.json)
#   make knulli-release  build the Knulli zip in dist/ (experimental, M20)
#   make emulation       run the pak in the userland of each platform in Docker (M20; LAYOUTS="h700 knulli")
#   make android-test    run the Android unit tests (:core with the shared vectors, :app) (JDK 17+, Android SDK)
#   make android-build   build the Android debug APK (android/app/build/outputs/apk/debug)
#   make docker-<target> run <target> inside the build image
#   make vX.Y.Z          release a version: bump pak.json, commit, tag and push
#   make vX.Y.Z-beta.N   pre-release (also -rc.N): tag and push only, pak.json unchanged
#   make ios-vX.Y.Z      release the iOS app (ios-vX.Y.Z-beta.N: TestFlight only, tag ios-beta-vX.Y.Z-beta.N); Xcode Cloud builds the tag
#   make release-checks  test the release scripts (pak tags, Xcode Cloud scripts, app release notes)

APP           := reminislink
PAK_NAME      := ReminisLink
# The NextUI platforms are all ARM64 and take the same binary, which reads
# PLATFORM at run time (milestone M20): the Pak Store installs the same zip
# into Tools/<platform>/ for every platform listed in pak.json. tg5050,
# h700 and my355 are experimental (docs/specs/05-console-pak.md §3.2).

PAK_VERSION   := $(shell sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' pak.json | head -n 1)
RELEASE_FILE  := $(shell sed -n 's/.*"release_filename": *"\([^"]*\)".*/\1/p' pak.json | head -n 1)
TAG           ?=
# Pak tags only (vX.Y.Z...): the app's ios-v* and ios-beta-v* tags never name a pak build.
VERSION       ?= $(shell git describe --tags --match 'v[0-9]*' --always --dirty 2>/dev/null || echo $(PAK_VERSION))

GO_SOURCES    := cmd internal protocol scripts
GO_VERSION    := $(shell sed -n 's/^go \([0-9.]*\)$$/\1/p' go.mod)
GO_PACKAGES   := ./...

BUILD_DIR     := build
DIST_DIR      := dist
PAK_DIR       := $(BUILD_DIR)/pak
BIN           := $(BUILD_DIR)/$(APP)
HOST_BIN      := $(BUILD_DIR)/reminislink-host
DESKTOP_BIN   := $(BUILD_DIR)/reminislink-desktop
RELEASE_ZIP   := $(DIST_DIR)/$(RELEASE_FILE)
# Knulli (experimental): unzipped into /userdata/roms/tools/.
KNULLI_DIR    := $(BUILD_DIR)/knulli
KNULLI_ZIP    := $(DIST_DIR)/ReminisLink.knulli.zip

# Cross-compilation settings (Debian 11 multiarch toolchain)
CROSS_ENV     := CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC=aarch64-linux-gnu-gcc \
                 PKG_CONFIG_PATH=/usr/lib/aarch64-linux-gnu/pkgconfig
SDL_GFX_LIB   := /usr/lib/aarch64-linux-gnu/libSDL2_gfx-1.0.so.0
# Bundled for h700 only, as the Pak Store does (pak/launch.sh).
SDL_TTF_LIB   := /usr/lib/aarch64-linux-gnu/libSDL2_ttf-2.0.so.0
# Bundled for my355 only: SDL2_gfx built by Debian 10 (glibc 2.17), see
# scripts/setup-toolchain.sh.
SDL_GFX_OLD_LIB := /usr/lib/aarch64-linux-gnu/glibc-2.28/libSDL2_gfx-1.0.so.0
# "sdl" builds the gabagool UI (internal/ui); without it the binary is
# headless only.
GO_TAGS       := nodefaultfont sdl
# The desktop window has no NextUI theme: keep gabagool's default font.
DESKTOP_TAGS  := sdl
# pakVersion (API, Bonjour TXT cv) is semver without the "v" of the tag.
PAK_SEMVER    = $(patsubst v%,%,$(VERSION))
LDFLAGS       = -s -w -X main.version=$(PAK_SEMVER)

# Extra options of "make headless" and "make desktop", e.g. HEADLESS_ARGS="--port 48000 --name Test"
HEADLESS_ARGS ?=
DESKTOP_ARGS  ?=

RELEASE_BRANCH ?= main
# Overridden by scripts/test-release-scripts.sh (a fake git).
GIT ?= git

DOCKER        ?= docker
DOCKER_IMAGE  ?= reminislink-build
# Pinned OpenAPI validator image (no global install).
REDOCLY_IMAGE ?= redocly/cli:1.34.5

# Duration of each fuzz target in "make fuzz".
FUZZTIME      ?= 10s
PATCH_FUZZERS := FuzzIPS FuzzIPSReference FuzzBPS FuzzUPS FuzzBPSNoChecksum FuzzUPSNoChecksum

.DEFAULT_GOAL := help
.PHONY: help test lint fuzz contract vectors headless desktop screenshots build pak release knulli-pak knulli-release emulation check-version release-checks clean docker-image android-test android-build FORCE

help: ## Show this help
	@grep -E '^[a-zA-Z_%-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "} {printf "  %-18s %s\n", $$1, $$2}'

test: ## Run unit tests (pure Go, no SDL needed)
	CGO_ENABLED=0 go test $(GO_PACKAGES)

lint: ## Check formatting and run go vet
	@test -z "$$(gofmt -l $(GO_SOURCES))" || { echo "Files need gofmt:"; gofmt -l $(GO_SOURCES); exit 1; }
	CGO_ENABLED=0 go vet $(GO_PACKAGES)

fuzz: ## Run each fuzz target of the patch parsers for FUZZTIME (short runs)
	$(foreach f,$(PATCH_FUZZERS),CGO_ENABLED=0 go test ./internal/patch -run '^$$' -fuzz '^$(f)$$' -fuzztime $(FUZZTIME) &&) true

contract: ## Validate the OpenAPI contract and check the test vectors (Go and Swift copies)
	$(DOCKER) run --rm -v "$(CURDIR)/protocol":/spec:ro -w /spec $(REDOCLY_IMAGE) \
		lint --config redocly.yaml openapi.yaml
	CGO_ENABLED=0 go run ./protocol/test-vectors/gen -check -out protocol/test-vectors
	scripts/sync-test-vectors.sh --check

vectors: ## Regenerate the test vectors and copy them to the Swift test resources
	CGO_ENABLED=0 go run ./protocol/test-vectors/gen -out protocol/test-vectors
	scripts/sync-test-vectors.sh

headless: ## Run the pak headless (server + Bonjour) on a temporary copy of testdata/sdcard
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build -o $(HOST_BIN) ./cmd/reminislink
	@tmp="$${TMPDIR:-/tmp}" && sd="$$(mktemp -d "$${tmp%/}/reminislink-sd.XXXXXX")" && \
		cp -R testdata/sdcard/. "$$sd/" && \
		echo "Fake SD card copied to $$sd (logs in $$sd/.userdata/tg5040/logs)" && \
		exec $(HOST_BIN) --headless --sd "$$sd" $(HEADLESS_ARGS)

desktop: ## Run the pak with its UI in a window on a temporary copy of testdata/sdcard (needs SDL2, SDL2_ttf, SDL2_image, SDL2_gfx)
	@pkg-config --exists sdl2 SDL2_ttf SDL2_image SDL2_gfx || { \
		echo "SDL2 development libraries not found (pkg-config sdl2 SDL2_ttf SDL2_image SDL2_gfx)."; \
		echo "On macOS: brew install pkg-config sdl2 sdl2_ttf sdl2_image sdl2_gfx"; exit 1; }
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 go build -tags "$(DESKTOP_TAGS)" -o $(DESKTOP_BIN) ./cmd/reminislink
	@tmp="$${TMPDIR:-/tmp}" && sd="$$(mktemp -d "$${tmp%/}/reminislink-sd.XXXXXX")" && \
		cp -R testdata/sdcard/. "$$sd/" && \
		echo "Fake SD card copied to $$sd (logs in $$sd/.userdata/tg5040/logs)" && \
		exec $(DESKTOP_BIN) --desktop --sd "$$sd" $(DESKTOP_ARGS)

screenshots: ## Regenerate docs/screenshots/ (desktop UI under Xvfb, in Docker)
	$(DOCKER) build -t reminislink-screenshots --build-arg GO_VERSION="$(GO_VERSION)" scripts/screenshots
	$(DOCKER) run --rm -v "$(CURDIR)":/repo:ro -v "$(CURDIR)/docs/screenshots":/out \
		-v reminislink-gomod:/root/go/pkg/mod reminislink-screenshots sh /repo/scripts/screenshots/run.sh

build: $(BIN) ## Cross-compile the ARM64 binary (gabagool UI included)

$(BIN): FORCE
	@mkdir -p $(BUILD_DIR)
	$(CROSS_ENV) go vet -tags "$(GO_TAGS)" ./cmd/... ./internal/ui/...
	$(CROSS_ENV) go build -tags "$(GO_TAGS)" -trimpath -ldflags "$(LDFLAGS)" -o $@ ./cmd/reminislink

pak: build ## Assemble the pak folder in build/pak
	rm -rf $(PAK_DIR)
	mkdir -p $(PAK_DIR)/resources/lib
	cp pak/launch.sh $(BIN) pak.json $(PAK_DIR)/
	# A pre-release leaves pak.json unchanged in the repository (the Pak
	# Store reads it there): only the copy in the zip names the tag.
	@if [ -n "$(TAG)" ]; then \
		sed "s/^\(  \"version\": *\"\)[^\"]*\"/\1$(TAG)\"/" pak.json > $(PAK_DIR)/pak.json; \
	fi
	cp README.md LICENSE $(PAK_DIR)/
	cp pak/THIRD_PARTY.md $(PAK_DIR)/resources/
	cp -L $(SDL_GFX_LIB) $(PAK_DIR)/resources/lib/
	mkdir -p $(PAK_DIR)/resources/lib/h700 $(PAK_DIR)/resources/lib/my355
	cp -L $(SDL_TTF_LIB) $(PAK_DIR)/resources/lib/h700/
	cp -L $(SDL_GFX_OLD_LIB) $(PAK_DIR)/resources/lib/my355/
	chmod +x $(PAK_DIR)/launch.sh $(PAK_DIR)/$(APP)

release: check-version pak ## Build the release zip (Pak Store format) in dist/
	rm -rf $(DIST_DIR)
	mkdir -p $(DIST_DIR)
	# The pak's contents sit at the root of the zip: the Pak Store creates
	# Tools/<PLATFORM>/ReminisLink.pak/ itself and extracts the zip into it
	# (a top "ReminisLink.pak/" folder would nest it and the pak would not start).
	# By hand, Finder and Windows extract it into a "ReminisLink.pak" folder.
	cd $(BUILD_DIR)/pak && zip -qr "$(CURDIR)/$(RELEASE_ZIP)" .
	@zip -sf "$(RELEASE_ZIP)" | grep -qx '  launch.sh' || { echo "launch.sh is not at the root of $(RELEASE_ZIP)"; exit 1; }
	@ls -l $(DIST_DIR)

knulli-pak: build ## Assemble the Knulli launcher and its hidden folder in build/knulli (experimental)
	rm -rf $(KNULLI_DIR)
	mkdir -p $(KNULLI_DIR)/.$(PAK_NAME)/lib
	cp pak/knulli/$(PAK_NAME).sh $(KNULLI_DIR)/
	cp $(BIN) README.md LICENSE $(KNULLI_DIR)/.$(PAK_NAME)/
	cp pak/THIRD_PARTY.md $(KNULLI_DIR)/.$(PAK_NAME)/
	cp -L $(SDL_GFX_LIB) $(KNULLI_DIR)/.$(PAK_NAME)/lib/
	chmod +x $(KNULLI_DIR)/$(PAK_NAME).sh $(KNULLI_DIR)/.$(PAK_NAME)/$(APP)

knulli-release: check-version knulli-pak ## Build the Knulli zip in dist/ (unzip into /userdata/roms/tools/; experimental)
	mkdir -p $(DIST_DIR)
	rm -f $(KNULLI_ZIP)
	cd $(KNULLI_DIR) && zip -qr "$(CURDIR)/$(KNULLI_ZIP)" $(PAK_NAME).sh .$(PAK_NAME)
	@ls -l $(KNULLI_ZIP)

emulation: ## Run the pak headless in the userland of each platform and Knulli (Docker, linux/arm64; LAYOUTS to choose)
	scripts/emulation/run.sh $(LAYOUTS)

check-version: ## Check TAG (when set, e.g. by the CI) against pak.json: version and changelog entry ("Unreleased" for a pre-release)
	@if [ -n "$(TAG)" ]; then \
		kind="$$(scripts/pak-release-kind.sh "$(TAG)")" || exit 1; \
		if [ "$$kind" = latest ]; then \
			[ "$(TAG)" = "$(PAK_VERSION)" ] || { echo "Tag $(TAG) does not match pak.json version $(PAK_VERSION)"; exit 1; }; \
			grep -q '^    "$(TAG)":' pak.json || { echo "pak.json has no changelog entry for $(TAG)"; exit 1; }; \
		else \
			grep -q '^    "Unreleased":' pak.json || { echo "Pre-release $(TAG): pak.json needs an \"Unreleased\" changelog entry (its release notes)"; exit 1; }; \
		fi; \
	fi

release-checks: ## Test the release scripts: pak tags, Xcode Cloud scripts, app release notes
	scripts/test-release-scripts.sh

v%: FORCE ## Release a version (make v1.2.3) or a pre-release (make v1.2.3-beta.1, -rc.1); pak.json needs its changelog entry
	@set -e; V="$@"; \
	scripts/pak-release-kind.sh "$$V" >/dev/null || exit 1; \
	[ "$$(git rev-parse --abbrev-ref HEAD)" = "$(RELEASE_BRANCH)" ] || { echo "Releases are made from $(RELEASE_BRANCH)"; exit 1; }; \
	[ -z "$$(git status --porcelain)" ] || { echo "The working tree has uncommitted changes"; exit 1; }; \
	if git rev-parse -q --verify "refs/tags/$$V" >/dev/null; then echo "Tag $$V already exists"; exit 1; fi; \
	grep -q "\"Unreleased\":" pak.json || grep -q "^    \"$$V\":" pak.json || { echo "Add a changelog entry (Unreleased or $$V) in pak.json first"; exit 1; }
	$(MAKE) lint test
	@set -e; V="$@"; \
	if [ "$$(scripts/pak-release-kind.sh "$$V")" = prerelease ]; then \
		grep -q '^    "Unreleased":' pak.json || { echo "A pre-release takes its notes from the \"Unreleased\" entry of pak.json: add it first"; exit 1; }; \
		git tag -a "$$V" -m "Pre-release $$V"; \
		git push origin "refs/tags/$$V"; \
		echo "Pre-release $$V tagged, pak.json unchanged: the CI will publish a GitHub pre-release, ignored by the Pak Store."; \
		exit 0; \
	fi; \
	sed "s/\"Unreleased\":/\"$$V\":/" pak.json > pak.json.tmp && mv pak.json.tmp pak.json; \
	sed "s/^\(  \"version\": *\"\)[^\"]*\"/\1$$V\"/" pak.json > pak.json.tmp && mv pak.json.tmp pak.json; \
	if ! git diff --quiet -- pak.json; then git commit -q -m "Release $$V" -- pak.json; fi; \
	git tag -a "$$V" -m "Release $$V"; \
	git push --atomic origin HEAD "refs/tags/$$V"; \
	echo "Released $$V: the CI will publish the GitHub release."

ios-v% ios-beta-v%: FORCE ## Release the iOS app (make ios-v1.2.3: App Review) or a TestFlight pre-release (make ios-v1.2.3-beta.1, tagged ios-beta-v1.2.3-beta.1); Xcode Cloud builds the tag
	@set -e; \
	parsed="$$(bash -c '. ios/ci_scripts/common.sh && T="$$(rl_tag_name "$$0")" && rl_parse_tag "$$T" && echo "$$RL_KIND $$RL_VERSION $$T"' "$@")" || { echo "Invalid app tag $@ (expected ios-vX.Y.Z, ios-vX.Y.Z-beta.N or ios-vX.Y.Z-rc.N)"; exit 1; }; \
	set -- $$parsed; KIND="$$1"; X="$$2"; V="$$3"; \
	[ "$$($(GIT) rev-parse --abbrev-ref HEAD)" = "$(RELEASE_BRANCH)" ] || { echo "Releases are made from $(RELEASE_BRANCH)"; exit 1; }; \
	[ -z "$$($(GIT) status --porcelain)" ] || { echo "The working tree has uncommitted changes"; exit 1; }; \
	if $(GIT) rev-parse -q --verify "refs/tags/$$V" >/dev/null; then echo "Tag $$V already exists"; exit 1; fi; \
	if [ "$$KIND" = beta ]; then \
		$(GIT) tag -a "$$V" -m "App pre-release $$V (TestFlight)"; \
		$(GIT) push origin "refs/tags/$$V"; \
		echo "Pre-release $$V tagged: Xcode Cloud (Beta workflow) sends version $$X to TestFlight."; \
		exit 0; \
	fi; \
	if ! ios/ci_scripts/release_notes.sh has "$$X"; then \
		ios/ci_scripts/release_notes.sh has Unreleased || { echo "Add a '## Unreleased' or '## $$X' section to ios/CHANGELOG.md first"; exit 1; }; \
		sed "s/^## Unreleased$$/## $$X/" ios/CHANGELOG.md > ios/CHANGELOG.md.tmp && mv ios/CHANGELOG.md.tmp ios/CHANGELOG.md; \
	fi; \
	ios/ci_scripts/release_notes.sh sync; \
	if ! $(GIT) diff --quiet -- ios/CHANGELOG.md ios/AppStore; then $(GIT) commit -q -m "Release app $$X" -- ios/CHANGELOG.md ios/AppStore; fi; \
	$(GIT) tag -a "$$V" -m "App release $$V"; \
	$(GIT) push --atomic origin HEAD "refs/tags/$$V"; \
	echo "Released app $$X: Xcode Cloud (Release workflow) builds it and prepares the App Store version."

# Android app (milestone M21): Gradle wrapper in android/. Needs a JDK (17+)
# and the Android SDK (ANDROID_HOME or android/local.properties).
GRADLEW       := cd android && ./gradlew --console=plain

android-test: ## Run the Android unit tests (:core reads protocol/test-vectors in place)
	$(GRADLEW) :core:test :app:testDebugUnitTest

android-build: ## Build the Android debug APK
	$(GRADLEW) :app:assembleDebug

clean: ## Remove build outputs
	rm -rf $(BUILD_DIR) $(DIST_DIR)

docker-image: ## Build the Docker build image
	$(DOCKER) build --platform linux/amd64 -t $(DOCKER_IMAGE) .

docker-%: docker-image ## Run a target inside the build image (e.g. make docker-release)
	$(DOCKER) run --rm --platform linux/amd64 \
		-v "$(CURDIR)":/src -w /src \
		-v reminislink-gomod:/root/go/pkg/mod \
		-v reminislink-gocache:/root/.cache/go-build \
		-e VERSION="$(VERSION)" \
		$(DOCKER_IMAGE) make $*

FORCE:
