.PHONY: bootstrap dev-up dev-down dev-logs lint test build integration-test
bootstrap dev-up dev-down dev-logs lint test build integration-test:
	node tools/task.mjs $@
