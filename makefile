BIN_DIR = bin
ENUMGEN_BINARY = $(BIN_DIR)/enumgen

.PHONY: build-tools
build-tools:
	@echo "🛠  Building internal tools..."
	@mkdir -p $(BIN_DIR)
	@go build -o $(ENUMGEN_BINARY) ./tools/enumgen

.PHONY: gen-enums
gen-enums: build-tools
	@echo "Generating Enums..."
	@# the tool at the root (.)
	@$(ENUMGEN_BINARY)
	@echo "Enums generated successfully!"
