#!/bin/bash
set -e

# Initialize DB if needed
airflow db migrate

# Airflow 3.0 SimpleAuthManager: set password from env to override auto-generated
python3 -c "
import json, os
pw_file = os.path.join(os.environ.get('AIRFLOW_HOME', '/opt/airflow'), 'simple_auth_manager_passwords.json.generated')
user = os.environ.get('AIRFLOW__SIMPLE_AUTH_MANAGER__USERS', 'admin')
password = os.environ.get('AIRFLOW__SIMPLE_AUTH_MANAGER__PASSWORDS', 'admin')
with open(pw_file, 'w') as f:
    json.dump({user: password}, f)
"

# Start API server, DAG processor, and scheduler
airflow api-server --port 8080 &
airflow dag-processor &
exec airflow scheduler
