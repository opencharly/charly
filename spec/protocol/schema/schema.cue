package protocol

#Field: {
	name:          string & !=""
	type:          string & !=""
	number:        int & >0 & <536870912
	doc?:          string
	repeated?:     bool
	optional?:     bool
	map_key_type?: string & !=""
	deprecated?:   bool
}

#Message: {
	name: string & !=""
	doc?: string
	fields?: [...#Field]
	reserved_names?: [...string]
	reserved_numbers?: [...int]
}

#Method: {
	name:              string & !=""
	request:           string & !=""
	response:          string & !=""
	doc?:              string
	client_streaming?: bool
	server_streaming?: bool
	deprecated?:       bool
}

#Service: {
	name: string & !=""
	doc?: string
	methods: [...#Method]
}

#Protocol: {
	syntax:     "proto3"
	package:    string & !=""
	go_package: string & !=""
	doc?:       string
	messages: [...#Message]
	services: [...#Service]
}

protocol: #Protocol
