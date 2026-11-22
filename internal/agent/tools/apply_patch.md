# apply_patch

Apply one patch to the working tree. The patch is a single string in the format below: it can add, update, move and delete several files in one call.

Every section must apply exactly. Crush resolves the whole patch before it writes anything, so a patch that fails on its third file leaves the first two alone, and it asks permission for each file it touches.

## Format

Start the patch with `*** Begin Patch` and end it with `*** End Patch`. Between them, one or more sections:

```
*** Begin Patch
*** Add File: path/to/new.txt
+first line
+second line
*** Update File: path/to/existing.txt
@@ optional heading naming the function or block
 unchanged context line
-line to remove
+line to add
*** Update File: path/to/old_name.txt
*** Move to: path/to/new_name.txt
@@
 alpha
-beta
+BETA
*** Delete File: path/to/gone.txt
*** End Patch
```

Rules that make the patch apply:

- `*** Add File: ` starts from nothing; every line of the new file starts with `+`.
- `*** Update File: ` needs one or more hunks. A hunk starts with `@@` and the lines under it start with a space (unchanged context), `-` (removed) or `+` (added).
- Give each hunk enough context to be found once: the three unchanged lines around a change are usually enough. Include the whole function signature when the changed lines are short.
- Context and removed lines are matched byte for byte against the file, indentation included. Read the file with `view` first and copy its exact lines.
- An update with `*** Move to: ` and no hunks renames the file.
- A `*** Update File: ` section without `*** Move to: ` must change the file; a patch that changes nothing is rejected.
- An `*** Add File: ` whose path exists, or a `*** Move to: ` whose destination exists, is rejected.
- Paths are relative to the working directory.

## Safety

- Updating, moving or deleting a file requires having read it in this session with `view` or `read`, and the file must not have changed on disk since that read.
- Each file gets its own permission prompt, showing the change as a diff.
- Prefer one patch over many small edits when a task touches several files.
