FROM apache/airflow:3.1.8-python3.12

USER root
RUN apt-get update && apt-get install -y --no-install-recommends \
    git curl docker.io && \
    rm -rf /var/lib/apt/lists/*

USER airflow
RUN pip install --no-cache-dir requests opensearch-py

COPY docker/airflow/entrypoint.sh /entrypoint.sh
USER root
RUN chmod +x /entrypoint.sh
USER airflow

ENTRYPOINT ["/entrypoint.sh"]
