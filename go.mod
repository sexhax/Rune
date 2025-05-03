module selfbot

go 1.24.2

require (
	github.com/gorilla/websocket v1.5.3
	github.com/hugolgst/rich-go v0.0.0-20240715122152-74618cc1ace2
	selfbot/rpc v0.0.0-00010101000000-000000000000
)

require (
	github.com/rikkuness/discord-rpc v0.0.0-20200829012113-11191b75b58a // indirect
	gopkg.in/natefinch/npipe.v2 v2.0.0-20160621034901-c1b8fa8bdcce // indirect
)

replace github.com/skifli/gocord => ./api

replace selfbot/rpc => ./rpc
