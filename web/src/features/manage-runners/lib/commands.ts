/**
 * The commands that join a machine to this instance as a runner (CA-01, CA-02): run the runner once
 * to approve it in the browser, then install the systemd user unit so it survives a reboot. They
 * are the same commands the CLI documents (docs/agents.md, "Runners").
 */
export const RUNNER_RUN_COMMAND = "partyline runner";
export const RUNNER_INSTALL_COMMAND = "partyline runner install";
export const RUNNER_CHECK_COMMAND = "partyline runner --check";
