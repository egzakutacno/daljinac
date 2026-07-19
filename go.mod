module agent

go 1.26

require golang.org/x/crypto v0.54.0

require (
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/term v0.45.0 // indirect
)

replace golang.org/x/crypto v0.54.0 => github.com/egzakutacno/go-crypto-ssh v0.54.0
