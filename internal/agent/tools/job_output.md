Get stdout/stderr from a background shell by ID; returns whatever the job has printed so far. Call it when you need the job's result, not right after starting the job, and set wait=true only when blocking until the job finishes is what you want.

You are notified in-session when a job finishes, so do not busy-poll or sleep on one. Use wait=true only when you are genuinely blocked on the result; otherwise keep working and collect it before your final answer.
