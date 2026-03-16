module.exports = {
	apps: [
		{
			name: "1sat-stack",
			script: "/opt/homebrew/bin/go",
			args: "run ./cmd/server --config config.test.yaml",
			cwd: "/Users/davidcase/Source/1sat/1sat-stack",
			env: {
				ONESAT_WALLET_MODE: "embedded",
			},
		},
	],
};
