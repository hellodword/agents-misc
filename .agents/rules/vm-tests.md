# VM Test Execution

- Select a prepared, authorized executor and registered target. Reuse an established project-local flow when it meets the task. Environment preparation belongs to a separately authorized setup task and its manual guide.
- Use the service's task IDs, scoped transfer URLs and artifact records. Retry identical submissions with the same ID; give new executions a fresh ID.
- Keep execution bounded by task deadlines. HTTP connection state is separate from task state; reconnect to inspect the original task before submitting another execution.
- Treat native input delivery and application success as separate observations. Inspect an unconfirmed delivery before sending another action. Keep capture identities and evidence errors alongside the task's cleanup result.
- Let active tasks finish before switching virtualization backends. Idle registered VMs may be shut down according to their configured policy. Confirm actual cleanup before releasing resources; preserve evidence when cleanup is unresolved.
- Use existing task authorization and the target's configured capabilities for VM settings, power and device operations. Read effective capabilities before use and treat policy rejection as an execution boundary.
- Retain useful failure logs, screenshots and reports before restoring a baseline. Report task identity, assertions, exit status, evidence locations and cleanup state. Describe graphics and media capabilities from the actual operation measured.
