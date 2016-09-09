NAME := polymer-server

WORKSPACE := $(PWD)/build/polymer
SORCE := $(WORKSPACE)/src

GOPATH := $(GOPATH):$(WORKSPACE)
export $(GOPATH)

all: $(NAME)

$(NAME): polymer/*.go polymerd/*.go
	mkdir -p $(SORCE)
	cp -r polymer $(SORCE)
	go build -o $(NAME) polymerd/main.go

clean:
	$(rm) -rf $(NAME) build


.PHONY: clean
