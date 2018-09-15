NAME := "torb.$(USER)"

all: build

.PHONY: clean
clean:
	rm -rf torb

deps:
	gb vendor restore

.PHONY: build
build:
	GOPATH=`pwd`:`pwd`/vendor go build -v torb

.PHONY: deploy
deploy:
	ssh root@isucon1 systemctl stop torb.go.service
	ssh root@isucon2 systemctl stop torb.go.service
	ssh root@isucon3 systemctl stop torb.go.service
	$(MAKE) put NAME=torb
	ssh root@isucon1 systemctl start torb.go.service
	ssh root@isucon2 systemctl start torb.go.service
	ssh root@isucon3 systemctl start torb.go.service

.PHONY: put
put: build
	scp $< root@isucon1:/home/isucon/torb/webapp/go/$(NAME)
	scp $< root@isucon3:/home/isucon/torb/webapp/go/$(NAME)
	scp $< root@isucon2:/home/isucon/torb/webapp/go/$(NAME)
