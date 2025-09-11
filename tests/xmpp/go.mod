module github.com/selfhoster1312/matterbridge-test/xmpp

go 1.25.1

replace github.com/xmppo/go-xmpp => ../../vendor/github.com/matterbridge/go-xmpp

require github.com/xmppo/go-xmpp v0.2.17

require (
	golang.org/x/crypto v0.42.0 // indirect
	golang.org/x/net v0.44.0 // indirect
)
