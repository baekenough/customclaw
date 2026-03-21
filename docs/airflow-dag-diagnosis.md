# Airflow DAG Diagnosis: 0 DAGs Showing in UI

## Root Cause

**The `airflow dag-processor` is not running.** In Airflow 3.0, DAG file processing was extracted into a standalone process that must run separately from the scheduler. The current entrypoint (`docker/airflow/entrypoint.sh`) only starts `api-server` and `scheduler`, but does NOT start the `dag-processor`.

Without the dag-processor, DAGs are never parsed from the filesystem (`/opt/airflow/dags/`) into the database. Since the Airflow 3.0 UI and `airflow dags list` both read DAGs from the database (not the filesystem), they show 0 DAGs.

### Evidence

1. Running `airflow dags reserialize` (which manually triggers DAG parsing) successfully finds both DAGs:
   ```
   Sync 2 DAGs
   Creating ORM DAG for claude_code_release_monitor
   Creating ORM DAG for omc_issue_analyzer
   ```

2. Running `airflow dag-processor` successfully discovers and parses both files:
   ```
   Searching for files in dags-folder at /opt/airflow/dags
   Found 2 files for bundle dags-folder
   ```

3. The scheduler log does NOT show any DAG parsing activity - it only manages execution.

4. `airflow dags list` without prior reserialize shows `Filling up the DagBag from database` and returns "No data found".

## Fix: Update Entrypoint

The entrypoint must also start the `dag-processor` process.

**File:** `docker/airflow/entrypoint.sh`

**Current (broken):**
```bash
#!/bin/bash
set -e

# Initialize DB if needed
airflow db migrate

# Create admin user if not exists
airflow users create \
    --username "${AIRFLOW_API_USER:-admin}" \
    --password "${AIRFLOW_API_PASSWORD:-admin}" \
    --firstname Admin \
    --lastname User \
    --role Admin \
    --email admin@customclaw.local 2>/dev/null || true

# Start API server and scheduler
airflow api-server --port 8080 &
exec airflow scheduler
```

**Fixed (add dag-processor):**
```bash
#!/bin/bash
set -e

# Initialize DB if needed
airflow db migrate

# Create admin user if not exists
airflow users create \
    --username "${AIRFLOW_API_USER:-admin}" \
    --password "${AIRFLOW_API_PASSWORD:-admin}" \
    --firstname Admin \
    --lastname User \
    --role Admin \
    --email admin@customclaw.local 2>/dev/null || true

# Start API server, DAG processor, and scheduler
airflow api-server --port 8080 &
airflow dag-processor &
exec airflow scheduler
```

The only change is adding `airflow dag-processor &` before the scheduler.

## Other Issues Found

### Import Paths Are Correct (Not the Problem)

All imports were verified to work correctly in the `apache/airflow:3.0.1-python3.12` container:

| Import | Status | Notes |
|--------|--------|-------|
| `from airflow.sdk import dag, task` | OK | Correct Airflow 3.x path |
| `from airflow.sdk import Variable` | OK | Correct Airflow 3.x path |
| `from airflow.models.param import Param` | OK | Works, but `from airflow.sdk import Param` is the preferred 3.x path |
| `from airflow.exceptions import AirflowFailException` | OK | Still valid in 3.x |
| `from airflow.utils.trigger_rule import TriggerRule` | OK | Still valid in 3.x |

### Recommended Import Improvement (Non-Critical)

`from airflow.models.param import Param` works but the idiomatic Airflow 3.x import is:

```python
from airflow.sdk import Param
```

This is a style improvement, not a fix for the 0 DAGs issue.

### DAG Instantiation Pattern Is Correct

Both DAGs use the correct pattern:
```python
@dag(...)
def my_dag_function():
    ...

dag_instance = my_dag_function()
```

This is valid in Airflow 3.x.

### No Module-Level Code Issues

Both DAG files define helper functions at module level, which is fine. There are no top-level side effects that would cause parsing failures.

## Version Note

Airflow 3.0.1 is significantly behind the current stable (3.1.8 as of 2026-03-10). Consider upgrading the Docker image to benefit from bug fixes and improvements.
