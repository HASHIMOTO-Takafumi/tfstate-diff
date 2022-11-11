COMMAND_NAME=tfstate-diff

build:
	cd cmd/$(COMMAND_NAME) && go build -ldflags "-s -w" -trimpath

clean:
	rm -f cmd/$(COMMAND_NAME)/$(COMMAND_NAME)
