all: app

app:
	go build -o ./go/app ./go/

app_linux_amd64:
	GOOS=linux GOARCH=amd64 go build -tags netgo -installsuffix netgo -o ./go/app_linux_amd64 ./go/
