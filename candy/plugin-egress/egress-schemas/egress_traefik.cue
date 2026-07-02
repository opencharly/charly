// Egress schema for the traefik dynamic-config charly GENERATES at
// .build/<box>/traefik-routes.yml (generateTraefikRoutes hand-builds it as YAML
// text). Validates the real invariants of the hand-assembled routers/services —
// non-empty Host rule, service name, and backend url — so a candy with an empty
// host/port produces a build error instead of a silently-broken proxy config.
// routers/services are null when no route candies are present (traefik composed
// but unused), so both tolerate null. Package-less → joins sharedCueSchema.
#TraefikRoutes: {
	http: {
		routers?: null | {[string]: {
			rule:    string & !=""
			service: string & !=""
			...
		}}
		services?: null | {[string]: {
			loadBalancer: {
				servers: [...{url: string & !="", ...}]
				...
			}
			...
		}}
		...
	}
	...
}
