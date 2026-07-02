package main

import "testing"

// TestEscapeContainerfileEnvValue locks in the contract that runtime-injected
// env vars (e.g. POSTGRES_PASSWORD from a podman secret) survive Docker's
// build-time ENV substitution by being escaped at emission. Without this,
// `${POSTGRES_PASSWORD}` references in env: block values get silently emptied
// by Docker (POSTGRES_PASSWORD isn't a build arg; the substitution resolves
// to empty), breaking every candy that composes a connection URL with a
// runtime password.
func TestEscapeContainerfileEnvValue(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"runtime password reference (the airflow case)",
			`echo "postgresql+psycopg2://airflow:${POSTGRES_PASSWORD}@127.0.0.1/airflow"`,
			`echo "postgresql+psycopg2://airflow:\${POSTGRES_PASSWORD}@127.0.0.1/airflow"`,
		},
		{
			"bare $VAR (no braces)",
			`echo $POSTGRES_PASSWORD`,
			`echo \$POSTGRES_PASSWORD`,
		},
		{
			"PATH preserved (Docker layer-cumulative substitution)",
			`/usr/local/bin:${PATH}`,
			`/usr/local/bin:${PATH}`,
		},
		{
			"PATH preserved alongside other escapes",
			`/usr/bin:$HOME/bin:${PATH}`,
			`/usr/bin:\$HOME/bin:${PATH}`,
		},
		{
			"no $ at all — pass-through",
			`postgresql:///airflow?host=/home/user/.postgresql`,
			`postgresql:///airflow?host=/home/user/.postgresql`,
		},
		{
			"empty string",
			"",
			"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := escapeContainerfileEnvValue(tc.in)
			if got != tc.want {
				t.Errorf("escapeContainerfileEnvValue(%q)\n  got:  %q\n  want: %q", tc.in, got, tc.want)
			}
		})
	}
}
