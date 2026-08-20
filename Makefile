BIN := servergo
ROOT := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))

# webview_go cgo direktivasida webkit2gtk-4.0 ni so'raydi, Ubuntu 25.10+ da esa
# faqat 4.1 bor. third_party/pkgconfig ichidagi shim shuni yo'naltiradi.
export PKG_CONFIG_PATH := $(ROOT)third_party/pkgconfig:$(PKG_CONFIG_PATH)

.PHONY: build run headless fmt vet clean deps install-desktop uninstall-desktop install-service uninstall-service install-cli uninstall-cli

build:
	go build -trimpath -ldflags="-s -w" -o $(BIN) .

run: build
	./$(BIN)

# Oynasiz — API ni curl bilan tekshirish uchun. Manzilni stdout ga chiqaradi.
headless: build
	./$(BIN) -headless

fmt:
	go fmt ./...

vet:
	go vet ./...

clean:
	rm -f $(BIN)

# webkit2gtk dev fayllari cgo uchun kerak (bir marta).
deps:
	sudo apt update
	sudo apt install -y libwebkit2gtk-4.1-dev libgtk-3-dev

install-desktop: build
	./scripts/install-desktop.sh

uninstall-desktop:
	./scripts/install-desktop.sh --remove

# Tunnellarni kompyuter yoqilganda (login'siz) ishga tushiruvchi systemd user servisi.
install-service: build
	./scripts/install-service.sh

uninstall-service:
	./scripts/install-service.sh --remove

# "servergo <buyruq>" ni terminaldan istalgan joydan chaqirish uchun
# ~/.local/bin ga bog'lama qo'yadi (sudo shart emas).
install-cli: build
	mkdir -p $(HOME)/.local/bin
	ln -sf $(ROOT)$(BIN) $(HOME)/.local/bin/$(BIN)
	@echo "Bog'lanma yaratildi: $(HOME)/.local/bin/$(BIN) -> $(ROOT)$(BIN)"
	@if ! echo "$$PATH" | tr ':' '\n' | grep -qx "$(HOME)/.local/bin"; then \
		echo; \
		echo "$(HOME)/.local/bin hali joriy PATH da yo'q."; \
		echo "Yangi terminal oching (yoki: source ~/.profile), so'ng 'servergo help' ishlaydi."; \
	fi

uninstall-cli:
	rm -f $(HOME)/.local/bin/$(BIN)
