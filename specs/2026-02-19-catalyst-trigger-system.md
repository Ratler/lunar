---
mode: sequential
complexity: complex
type: feature
playwright: false
created: 2026-02-19T19:00:00
---

# Plan: CATALYST Trigger System for Lunar

## Task Description

Implement a CATALYST trigger system for Lunar's package manager that allows modules to declare reactive behavior when other modules are installed or removed. This is analogous to how DEPENDS declares dependency relationships, but CATALYST declares event-driven reactions: "when module X is installed, rebuild me" or "when module Y is removed, disable my optional dep on Y and rebuild."

The CATALYST file lives in the moonbase module directory (alongside DETAILS, DEPENDS, BUILD, etc.) and is owned by the **reacting** module. Triggers only fire if the reacting module is currently installed. A pre-built cache (`catalyst.cache`) provides O(1) lookups at runtime via a bash associative array, and is rebuilt during moonbase updates.

This feature addresses 22+ modules in the moonbase that currently work around circular dependencies by either commenting out optional deps entirely (losing functionality) or relying on error-prone "Say NO on first install" user instructions.

## Objective

When complete:
1. Module maintainers can add a `CATALYST` file to any module to declare trigger rules
2. Running `lin <module>` or `lrm <module>` automatically detects and fires matching triggers for installed reactor modules
3. The `enable`/`disable` actions allow surgical flipping of optional dependency state, solving the circular dependency problem without full reconfiguration
4. A pre-built cache ensures trigger lookups are O(1) at runtime
5. The cache is rebuilt during moonbase updates and self-heals via lazy invalidation

## Problem Statement

Lunar has no mechanism for cross-module reactive behavior. When installing the Linux kernel (`lin linux`), there is no way for the nvidia driver module to automatically know it should rebuild itself. Currently:
- 15 modules have circular dependencies commented out entirely (losing functionality)
- 7 modules use `${PROBLEM_COLOR}Say NO on first install${DEFAULT_COLOR}` warnings in optional_depends messages (error-prone, manual)
- POST_INSTALL scripts in some modules (openssl, python, linux) contain hardcoded `lin -c` calls as a workaround

The CATALYST system provides a general, declarative mechanism for these cross-module reactions.

## Solution Approach

**Direct Integration** — hook into `lin` and `lrm` at known lifecycle points:
- In `lin`: before `lin_module` (pre-events fire immediately), after `lin_module` (post-events collected), after all modules complete (collected triggers deduplicated and fired)
- In `lrm`: same pattern with `on_pre_remove`/`on_remove`
- New `libs/catalyst.lunar` library contains all trigger logic
- Cache rebuilt alongside `depends.cache` in the moonbase update chain

### CATALYST File Format

One rule per line, `#` comments. Format: `<event> <watched_module> <action> [param]`

```bash
# nvidia/CATALYST — rebuild when kernel changes
on_install linux lin

# freetype2/CATALYST — enable harfbuzz support when available
on_install harfbuzz enable
on_install harfbuzz lin

# tiff/CATALYST — toggle libwebp support based on availability
on_install libwebp enable
on_install libwebp lin
on_remove libwebp disable
on_remove libwebp lin
```

### Events

| Event | When it fires |
|-------|------|
| `on_install` | After the watched module is fully installed and registered |
| `on_pre_install` | Before `lin` begins building the watched module |
| `on_remove` | After the watched module is fully removed |
| `on_pre_remove` | Before `lrm` begins removing the watched module |

### Actions

| Action | What it does | Optional param |
|--------|-------------|---------------|
| `lin` | `lin <reactor_module>` — rebuild | - |
| `lrm` | `lrm <reactor_module>` — remove | - |
| `fix` | `lunar fix <reactor_module>` — integrity check | - |
| `enable` | Flip optional dep to 'on' in DEPENDS_STATUS | dep name (defaults to watched module) |
| `disable` | Flip optional dep to 'off' in DEPENDS_STATUS | dep name (defaults to watched module) |
| `exec` | Run named script from module's moonbase dir | script filename (required) |

### Environment Variables

When a CATALYST action fires, two environment variables are exported so the reactor's BUILD (or exec script) knows which module triggered the action:

- `CATALYST_MODULE` — the name of the watched module that caused the trigger (e.g., `linux-lts`)
- `CATALYST_MODULE_VERSION` — the version of the watched module (e.g., `6.12.1`)

These are populated from the watched module's DETAILS file (via `run_details`) before firing the action. They are **cleared after each `lin_module` completes** in the reactor to prevent stale values leaking into non-catalyst builds.

Example use case: nvidia has two CATALYST lines watching `linux` and `linux-lts`. When `lin linux-lts` completes, nvidia's BUILD script can use `$CATALYST_MODULE` to find the right kernel source tree at `/usr/src/linux-lts-$CATALYST_MODULE_VERSION/`.

```bash
# nvidia/CATALYST
on_install linux lin
on_install linux-lts lin

# nvidia/BUILD (excerpt)
if [ -n "$CATALYST_MODULE" ]; then
  KERNEL_SRC="/usr/src/${CATALYST_MODULE}-${CATALYST_MODULE_VERSION}"
else
  # Normal build — user-selected kernel source
  KERNEL_SRC="/usr/src/linux-$(uname -r)"
fi
```

### Execution Rules

- `enable`/`disable` actions always execute before `lin`/`lrm`/`fix`/`exec`
- Post-events (`on_install`, `on_remove`) are collected, deduplicated, and fired after all modules complete (default). `--catalyst-immediate` flag fires after each module instead.
- Pre-events (`on_pre_install`, `on_pre_remove`) always fire immediately
- Triggers only fire if the reactor module is currently installed
- `on_remove`/`on_pre_remove` do NOT fire during `lin -u` upgrades (`UPGRADE=on`)
- `lin` action on held modules is skipped with a warning
- A visited set prevents infinite trigger loops (exact `watched:event:reactor:action` tuples tracked)
- `CATALYST_MODULE` and `CATALYST_MODULE_VERSION` are set before each action and cleared after each reactor's `lin_module` completes

### Cache Format

Flat file at `$STATE_DIRECTORY/catalyst.cache`:
```
watched_module:event:reactor_module:action:param
```
Example:
```
linux:on_install:nvidia:lin:
harfbuzz:on_install:freetype2:enable:
harfbuzz:on_install:freetype2:lin:
libwebp:on_install:tiff:enable:
libwebp:on_install:tiff:lin:
libwebp:on_remove:tiff:disable:
libwebp:on_remove:tiff:lin:
```

Loaded into bash associative array at `lin`/`lrm` startup. Key: `"watched:event"`, Value: newline-separated `"reactor:action:param"` entries.

## Relevant Files

- `libs/depends.lunar` — Contains `create_depends_cache()` which is the closest template for `create_catalyst_cache()`. Also contains `add_depends()` and dependency state manipulation functions that `enable`/`disable` actions will mirror.
- `libs/modules.lunar` — Contains `create_module_index()`, `check_module_index()` for lazy cache invalidation, `module_installed()` for checking reactor state, `has_module_file()` and `run_module_file()` for file dispatch.
- `libs/moonbase.lunar` — Contains `get_moonbase()` with the cache rebuild chain where `create_catalyst_cache` will be added.
- `libs/locking.lunar` — Contains `lock_file()`/`unlock_file()` used for atomic state file updates.
- `prog/lin` — Main install program. `main()` function (lines 64-251) has the multi-module loop where trigger collection/firing will be integrated.
- `prog/lrm` — Main remove program. Module removal loop (lines 302-327) where trigger collection/firing will be integrated.
- `etc/config` — Configuration file where `CATALYST_CACHE` path variable will be added.
- `Makefile` — Install targets where `catalyst.lunar` will be added to the libs install list.

### New Files

- `libs/catalyst.lunar` — New library containing all CATALYST trigger functions: cache creation, cache loading, trigger collection, deduplication, firing, enable/disable dep manipulation, and visited set management.

## Implementation Phases

### Phase 1: Foundation
Add configuration, create the catalyst.lunar library with cache creation and loading functions. Update the Makefile to install the new library.

### Phase 2: Core Implementation
Implement trigger collection, deduplication, enable/disable dep actions, action execution, and visited set loop protection.

### Phase 3: Integration & Polish
Hook into lin, lrm, and the moonbase update chain. Add the `--catalyst-immediate` flag. Add lazy cache invalidation to `check_module_index`.

## Step by Step Tasks

### 1. Add CATALYST_CACHE configuration
- **Task ID**: add-config
- **Depends On**: none
- **Description**:
  - Add `CATALYST_CACHE=$STATE_DIRECTORY/catalyst.cache` to `etc/config`, alongside the existing `DEPENDS_CACHE` definition
  - Follow the existing naming and formatting conventions in the config file
- **Tests**: N/A (config-only change)

### 2. Create catalyst.lunar with cache creation
- **Task ID**: create-cache-functions
- **Depends On**: add-config
- **Description**:
  - Create `libs/catalyst.lunar` with the standard header (matching style of other `.lunar` files)
  - Implement `create_catalyst_cache()`:
    - Fast path: if moonbase install log exists, grep it for `CATALYST` file paths
    - Slow path: `find $MOONBASE -type f -name CATALYST`
    - For each CATALYST file found, parse lines (skip comments/blank lines), extract module name from path
    - Output format: `watched_module:event:reactor_module:action:param` (one line per rule)
    - Write atomically using `temp_create`, `lock_file`, `install -m644`, `unlock_file` (same pattern as `create_module_index`)
  - Implement validation: skip lines with unknown events or actions, emit `debug_msg` warning
  - Add `debug_msg` calls throughout for traceability with `lin -d`
- **Tests**: N/A (no test suite exists; verification via manual `create_catalyst_cache` call on a test moonbase with CATALYST files, checking output format in catalyst.cache)

### 3. Implement cache loading into associative array
- **Task ID**: load-cache
- **Depends On**: create-cache-functions
- **Description**:
  - Implement `load_catalyst_cache()` in `libs/catalyst.lunar`:
    - Declare `declare -gA CATALYST_TRIGGERS` (global associative array)
    - Read `$CATALYST_CACHE` line by line with `IFS=: read -r watched event reactor action param`
    - Build key as `"$watched:$event"`, append `"$reactor:$action:$param"` to value (newline-separated)
    - If cache file doesn't exist, silently return (no triggers available)
  - Add `debug_msg` showing count of loaded trigger rules
- **Tests**: N/A (no test suite; verification via sourcing catalyst.lunar and calling `load_catalyst_cache`, then inspecting `${CATALYST_TRIGGERS[@]}`)

### 4. Implement trigger collection and deduplication
- **Task ID**: collect-triggers
- **Depends On**: load-cache
- **Description**:
  - Implement `collect_catalyst()` accepting `$1=module` and `$2=event`:
    - Look up `CATALYST_TRIGGERS["$1:$2"]`
    - For each matching entry, check if reactor module is installed via `module_installed`
    - For pre-events (`on_pre_install`, `on_pre_remove`): call `fire_catalyst_action` immediately
    - For post-events (`on_install`, `on_remove`): add to `CATALYST_QUEUE` array
    - Separate `enable`/`disable` entries into `CATALYST_DEP_QUEUE` (these run before other actions)
  - Implement `fire_collected_catalysts()`:
    - Process `CATALYST_DEP_QUEUE` first (all enable/disable actions)
    - Deduplicate `CATALYST_QUEUE` — same `reactor:action` pair only fires once
    - Fire each remaining action via `fire_catalyst_action`
  - Implement `reset_catalyst_queues()` to clear both queues (called at start of lin/lrm session)
- **Tests**: N/A (no test suite; verified through integration testing in tasks 7-8)

### 5. Implement visited set for loop protection
- **Task ID**: visited-set
- **Depends On**: collect-triggers
- **Description**:
  - Implement visited set using a temp file (survives across sub-process `lin` invocations):
    - `init_catalyst_visited()`: if `$CATALYST_VISITED_FILE` env var is set, reuse it; otherwise create via `temp_create "catalyst_visited"` and export the env var
    - `check_catalyst_visited()` accepting `$1=watched:event:reactor:action`: grep the visited file, return 0 if found (already visited), 1 if not
    - `mark_catalyst_visited()` accepting same args: append to visited file
    - `cleanup_catalyst_visited()`: remove temp file if we created it (check a flag `CATALYST_VISITED_OWNER`)
  - Integrate into `fire_catalyst_action`: before firing, check visited set; after firing, mark visited
  - The visited file is inherited by child `lin` processes through the exported env var
- **Tests**: N/A (no test suite; verified through deliberate loop creation in integration testing)

### 6. Implement enable/disable dep actions
- **Task ID**: enable-disable
- **Depends On**: visited-set
- **Description**:
  - Implement `catalyst_enable_dep()` accepting `$1=reactor_module` and `$2=dep_module`:
    - Lock `$DEPENDS_STATUS` and `$DEPENDS_STATUS_BACKUP` using `lock_file`
    - Find the line matching `^$1:$2:off:optional:` in `$DEPENDS_STATUS_BACKUP`
    - Replace `off` with `on` using awk (same pattern as `change_module_state` in modules.lunar)
    - Write to backup first, then copy to primary (standard backup-file protocol)
    - Unlock both files
    - `debug_msg` the change
    - If no matching line found (dep not in DEPENDS_STATUS), emit warning and return
  - Implement `catalyst_disable_dep()` — reverse: find `^$1:$2:on:optional:`, replace `on` with `off`
  - Both functions must be safe to call when the dep is already in the target state (no-op with debug_msg)
- **Tests**: N/A (no test suite; verified by inspecting DEPENDS_STATUS after calling enable/disable on a known optional dep)

### 7. Implement action execution dispatcher
- **Task ID**: action-dispatcher
- **Depends On**: enable-disable
- **Description**:
  - Implement `fire_catalyst_action()` accepting `$1=reactor`, `$2=action`, `$3=param`, `$4=watched_module`, `$5=event`:
    - Check visited set first; skip if already visited
    - Mark as visited
    - Before dispatching any action, set the catalyst environment variables:
      - Call `run_details $watched_module` (in a subshell to avoid polluting current env) to get the watched module's VERSION
      - `export CATALYST_MODULE="$watched_module"`
      - `export CATALYST_MODULE_VERSION="$watched_version"` (captured from subshell)
    - Dispatch based on action:
      - `lin`: check if reactor is held (`module_held`), skip with warning if so; otherwise `verbose_msg` and call `lin $reactor`
      - `lrm`: `verbose_msg` and call `lrm $reactor`
      - `fix`: `verbose_msg` and call `lunar fix $reactor`
      - `enable`: call `catalyst_enable_dep $reactor ${param:-$watched_module}`
      - `disable`: call `catalyst_disable_dep $reactor ${param:-$watched_module}`
      - `exec`: find the script in the reactor's moonbase directory (`$MOONBASE/$SECTION/$reactor/$param`), check it exists, source it in a subshell
      - Unknown action: `debug_msg` warning, skip
    - After the action completes, clear the catalyst env vars: `unset CATALYST_MODULE CATALYST_MODULE_VERSION`
    - Log to `debug_msg` before and after each action
    - After `lin`/`lrm`/`fix`/`exec` actions complete, call `collect_catalyst $reactor $resulting_event` to check for cascading triggers (using the same visited set)
  - For the `exec` action, the script runs in a subshell with `MODULE`, `VERSION`, `SECTION`, `SCRIPT_DIRECTORY` set (same env as `run_module_file`), plus `CATALYST_MODULE` and `CATALYST_MODULE_VERSION`
  - Implement helper `get_module_version()` that sources DETAILS in a subshell and echoes VERSION, to avoid polluting the caller's environment
- **Tests**: N/A (no test suite; verified through integration testing in tasks 8-9)

### 8. Integrate into lin
- **Task ID**: integrate-lin
- **Depends On**: action-dispatcher
- **Description**:
  - In `prog/lin`, add to startup (after sourcing `$BOOTSTRAP`, near existing initialization):
    - Call `load_catalyst_cache`
    - Call `init_catalyst_visited`
  - Add `--catalyst-immediate` flag parsing alongside existing flags (e.g., near `--deps`, `--compile`, etc.):
    - Sets `CATALYST_IMMEDIATE=1`
  - In `main()`, multi-module loop (around line 167 where `SINGLE_MODULE=1 main $MODULE` is called):
    - BEFORE the `lin_module` call: `collect_catalyst "$MODULE" on_pre_install`
    - AFTER successful `lin_module` return:
      - If `CATALYST_IMMEDIATE=1`: fire immediately via `fire_collected_catalysts`
      - Otherwise: just `collect_catalyst "$MODULE" on_install` (adds to queue)
  - After the multi-module loop completes (after line 167's loop ends), add:
    - `fire_collected_catalysts` (only if not in `--catalyst-immediate` mode, since those already fired)
  - Skip trigger collection entirely when `UPGRADE=on` (the `lrm -u` path) — check for this in `collect_catalyst`
  - After all triggers have fired, call `cleanup_catalyst_visited`
  - Ensure `load_catalyst_cache` is called after `reload_plugins` in the SIGUSR1 trap handler (or verify the array persists across trap handling)
- **Tests**: N/A (no test suite; manual verification: create zlocal test modules with CATALYST files, run `lin -d` and verify debug output shows trigger detection and firing)

### 9. Integrate into lrm
- **Task ID**: integrate-lrm
- **Depends On**: integrate-lin
- **Description**:
  - In `prog/lrm`, add to startup (after sourcing `$BOOTSTRAP`):
    - Call `load_catalyst_cache`
    - Call `init_catalyst_visited`
  - In the module removal loop (around lines 302-327):
    - BEFORE `lrm_module`: `collect_catalyst "$MODULE" on_pre_remove`
    - AFTER successful `lrm_module`: `collect_catalyst "$MODULE" on_remove`
  - Skip trigger collection when `UPGRADE=on` (check at the top of `collect_catalyst`)
  - After all removals complete: `fire_collected_catalysts`
  - After all triggers fired: `cleanup_catalyst_visited`
- **Tests**: N/A (no test suite; manual verification: create zlocal test module with CATALYST `on_remove` rule, run `lrm -d` and verify trigger fires)

### 10. Integrate cache rebuild into moonbase update
- **Task ID**: integrate-moonbase
- **Depends On**: integrate-lrm
- **Description**:
  - In `libs/moonbase.lunar`, in `get_moonbase()` (around line 82-88), add `create_catalyst_cache` to the rebuild chain:
    ```
    create_module_index &&
    create_depends_cache &&
    create_catalyst_cache &&
    update_depends &&
    update_plugins &&
    display_moonbase_changes
    ```
  - In `libs/modules.lunar`, in `check_module_index()` (around lines 141-166), add lazy invalidation:
    - Check if any CATALYST file in moonbase is newer than `$CATALYST_CACHE`
    - If so, call `create_catalyst_cache`
    - Use the same `find` pattern as the DEPENDS cache check but with `-name CATALYST`
- **Tests**: N/A (no test suite; manual verification: add a CATALYST file to a zlocal module, verify `check_module_index` triggers rebuild, verify `lget moonbase` includes catalyst cache in rebuild chain)

### 11. Update Makefile
- **Task ID**: update-makefile
- **Depends On**: integrate-moonbase
- **Description**:
  - Add `catalyst.lunar` to the libs install target in the Makefile
  - Follow the existing pattern used for other `.lunar` files (look at how `depends.lunar`, `modules.lunar`, etc. are installed)
  - Ensure the file is installed to `$(DESTDIR)/var/lib/lunar/functions/catalyst.lunar`
- **Tests**: N/A (config-only; verified by running `make DESTDIR=/tmp/test-install install` and checking the file exists)

### 12. Code Review
- **Task ID**: review-all
- **Depends On**: add-config, create-cache-functions, load-cache, collect-triggers, visited-set, enable-disable, action-dispatcher, integrate-lin, integrate-lrm, integrate-moonbase, update-makefile
- **Description**:
  Review all code changes for correctness, style, edge cases, and security. Re-read every file changed:
  - `libs/catalyst.lunar` — verify all functions follow codebase style (snake_case, UPPER_CASE globals, `local` declarations, `debug_msg` throughout). Check for:
    - Correct file locking in enable/disable (lock before read, unlock after write)
    - Proper quoting of variables (especially module names with special characters)
    - Atomic file writes (backup-file protocol)
    - Correct associative array syntax (bash 4+ required)
    - No command injection risks in `exec` action (script path validation)
  - `prog/lin` — verify trigger collection/firing is at correct points in the module loop. Check:
    - `UPGRADE=on` correctly skips triggers
    - `--catalyst-immediate` flag doesn't interfere with existing flags
    - Visited set cleanup happens even on error paths
  - `prog/lrm` — same checks as lin
  - `libs/moonbase.lunar` — verify `create_catalyst_cache` is in correct position in the chain
  - `libs/modules.lunar` — verify lazy invalidation logic is correct (CATALYST file newer than cache)
  - `etc/config` — verify variable naming consistency
  - `Makefile` — verify install path is correct
  Fix any issues found before proceeding to validation.
- **Tests**: N/A

### 13. Final Validation
- **Task ID**: validate-all
- **Depends On**: review-all
- **Description**:
  Run all validation commands and verify every acceptance criterion is met:
  - Verify `libs/catalyst.lunar` exists and contains all required functions
  - Verify `etc/config` contains `CATALYST_CACHE` variable
  - Verify `Makefile` includes `catalyst.lunar` in install targets
  - Verify `prog/lin` sources catalyst library and has trigger collection/firing hooks
  - Verify `prog/lrm` sources catalyst library and has trigger collection/firing hooks
  - Verify `libs/moonbase.lunar` calls `create_catalyst_cache` in rebuild chain
  - Verify `libs/modules.lunar` has CATALYST lazy invalidation in `check_module_index`
  - Verify no syntax errors: `bash -n libs/catalyst.lunar`, `bash -n prog/lin`, `bash -n prog/lrm`
  - Verify code style consistency: functions use snake_case, globals use UPPER_CASE
  - Verify all functions have `debug_msg` as first line
  - Verify file locking is used correctly in enable/disable functions

## Documentation Requirements

- Inline comments in `libs/catalyst.lunar` explaining:
  - The CATALYST file format (events, actions, params)
  - The cache format and rebuild triggers
  - The associative array structure
  - The visited set mechanism
  - The collect/deduplicate/fire lifecycle
- Comment block at the top of `libs/catalyst.lunar` matching the style of other `.lunar` files (copyright, description)
- Comments in `prog/lin` and `prog/lrm` at each integration point explaining what the catalyst calls do

## Acceptance Criteria

1. A module with a `CATALYST` file containing `on_install <module> lin` causes the reactor to rebuild when the watched module is installed (and reactor is installed)
2. A module with `on_install <module> enable` + `on_install <module> lin` correctly flips the optional dep to "on" and rebuilds
3. A module with `on_remove <module> disable` + `on_remove <module> lin` correctly flips the optional dep to "off" and rebuilds
4. Pre-events (`on_pre_install`, `on_pre_remove`) fire immediately before the watched module's action begins
5. Post-events are collected, deduplicated, and fired after all modules in a multi-module `lin`/`lrm` session complete
6. `--catalyst-immediate` flag causes post-events to fire after each individual module
7. Triggers do NOT fire during `lin -u` upgrades
8. `lin` action on held modules is skipped with a warning message
9. Deliberate trigger loops terminate (visited set prevents infinite recursion)
10. `create_catalyst_cache` produces correct flat-file output from CATALYST files in moonbase
11. `load_catalyst_cache` populates the associative array for O(1) lookups
12. Cache is rebuilt during `lget moonbase` (moonbase update)
13. Cache is lazily invalidated when CATALYST files are newer than the cache
14. `bash -n libs/catalyst.lunar` passes without syntax errors
15. All functions include `debug_msg` calls for traceability with `lin -d`
16. `CATALYST_MODULE` and `CATALYST_MODULE_VERSION` are set before each action fires and cleared after each action completes
17. `CATALYST_MODULE`/`CATALYST_MODULE_VERSION` are not present during normal (non-catalyst) builds

## Validation Commands

```bash
# Syntax check all modified/new files
bash -n libs/catalyst.lunar
bash -n prog/lin
bash -n prog/lrm

# Verify catalyst.lunar contains required functions
grep -c 'create_catalyst_cache\|load_catalyst_cache\|collect_catalyst\|fire_collected_catalysts\|fire_catalyst_action\|catalyst_enable_dep\|catalyst_disable_dep\|init_catalyst_visited\|check_catalyst_visited\|mark_catalyst_visited\|cleanup_catalyst_visited\|reset_catalyst_queues' libs/catalyst.lunar

# Verify config has CATALYST_CACHE
grep 'CATALYST_CACHE' etc/config

# Verify Makefile installs catalyst.lunar
grep 'catalyst.lunar' Makefile

# Verify lin integration
grep -c 'load_catalyst_cache\|collect_catalyst\|fire_collected_catalysts\|catalyst-immediate\|cleanup_catalyst_visited' prog/lin

# Verify lrm integration
grep -c 'load_catalyst_cache\|collect_catalyst\|fire_collected_catalysts\|cleanup_catalyst_visited' prog/lrm

# Verify moonbase integration
grep 'create_catalyst_cache' libs/moonbase.lunar

# Verify lazy invalidation
grep 'CATALYST' libs/modules.lunar

# Verify CATALYST_MODULE env var handling in action dispatcher
grep -c 'CATALYST_MODULE\|CATALYST_MODULE_VERSION\|get_module_version' libs/catalyst.lunar
```

## Notes

- **Bash 4+ required** for associative arrays (`declare -A`). Lunar Linux targets modern systems so this should not be an issue, but verify the minimum bash version in the bootstrap.
- **The `exec` action is the most security-sensitive** — the script path must be validated to exist within the module's moonbase directory. Never allow path traversal.
- **The `enable`/`disable` actions modify `DEPENDS_STATUS`** which is a shared state file. File locking is critical to avoid corruption when multiple processes run concurrently.
- **The visited set temp file** must be cleaned up on all exit paths (normal, error, signal). Consider adding cleanup to the existing trap handlers in `lin`/`lrm`.
- **AUTORESURRECT** — when a module is resurrected from cache (not rebuilt from source), `on_install` triggers should still fire since the module IS being installed. Verify the trigger collection point in `lin` covers both the build path and the resurrect path.
- **This feature does not modify any moonbase modules** — CATALYST files will be added to moonbase-core and moonbase-other in separate commits after this infrastructure is in place.
