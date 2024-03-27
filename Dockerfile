FROM golang:1.22.1-alpine3.19 AS build
WORKDIR /src
COPY . .
RUN go mod download
RUN ls .
RUN go build -o color-run ./cmd/main.go
# -ldflags="-X 'main.Version=`cat ${VERSION}`'"

FROM alpine
RUN apk update
# RUN apk upgrade
RUN apk add --no-cache ffmpeg
COPY --from=build /src/color-run /usr/bin/color-run
CMD ["color-run"]