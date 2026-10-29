Get stdout/stderr from a background shell by ID; set wait=true to block until completion.

You are notified in-session when a job finishes, so do not busy-poll or sleep on one. Use wait=true only when you are genuinely blocked on the result; otherwise keep working and collect it before your final answer.