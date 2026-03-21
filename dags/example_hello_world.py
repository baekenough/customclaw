"""
Example DAG — Hello World.

A minimal DAG to verify your Airflow setup is working correctly.
Place your own DAG files in the dags/ directory.

Graph:
    hello_task
"""

from datetime import datetime, timedelta

from airflow.sdk import dag, task


@dag(
    dag_id="example_hello_world",
    description="A simple DAG to verify Airflow is running",
    schedule=None,
    start_date=datetime(2024, 1, 1),
    catchup=False,
    default_args={
        "retries": 1,
        "retry_delay": timedelta(minutes=1),
    },
    tags=["example"],
)
def example_hello_world():

    @task
    def hello():
        """Print a greeting to verify the DAG executes."""
        print("Hello from CustomClaw! Your Airflow setup is working.")
        return {"status": "ok", "message": "Hello World"}

    hello()


example_hello_world()
