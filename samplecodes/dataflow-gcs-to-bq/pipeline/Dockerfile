# Dataflow Flex Template (Python) 用イメージ
#
# ハマりどころ:
#   apache_beam.transforms.sql.SqlTransform は cross-language transform で、
#   バックエンドが Java の Calcite SQL に展開される。標準の Python ベースイメージには
#   JRE が入っていないため、明示的に openjdk を入れないと SqlTransform が
#   "Could not start the expansion service" 系で失敗する。

FROM gcr.io/dataflow-templates-base/python311-template-launcher-base

ARG WORKDIR=/dataflow/template
WORKDIR ${WORKDIR}

# SqlTransform 用に JRE を入れる
RUN apt-get update \
    && apt-get install -y --no-install-recommends openjdk-17-jre-headless \
    && rm -rf /var/lib/apt/lists/*

ENV JAVA_HOME=/usr/lib/jvm/java-17-openjdk-amd64

COPY requirements.txt .
COPY pipeline.py .
COPY transforms.py .
COPY metadata.json .

ENV FLEX_TEMPLATE_PYTHON_PY_FILE="${WORKDIR}/pipeline.py"
ENV FLEX_TEMPLATE_PYTHON_REQUIREMENTS_FILE="${WORKDIR}/requirements.txt"

# requirements を pre-install して起動を高速化
RUN pip install --no-cache-dir -U pip \
    && pip install --no-cache-dir -r requirements.txt
