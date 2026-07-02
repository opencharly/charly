module github.com/opencharly/charly/candy/plugin-service

go 1.26.0

require github.com/opencharly/charly/charly v0.0.0

require (
	github.com/kr/pretty v0.3.1 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	gopkg.in/check.v1 v1.0.0-20180628173108-788fd7840127 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// Local build: charly's git-repo plugin loader builds this on the host against the
// in-tree charly (proto + sdk). A published external plugin would require a tagged
// charly version instead.
replace github.com/opencharly/charly/charly => ../../charly
