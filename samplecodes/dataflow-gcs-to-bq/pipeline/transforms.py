"""Dataflow GCS->BQ 検証ワークスペース 共通変換ロジック.

責務はピュアな ETL のみ:
- 入力ファイル形式は呼び出し側 (pipeline.py) で吸収する
- ここでは「dict 化された行」を InputRow (NamedTuple) に変換し、
  6 ブランチ (SQL × 3 + Python × 3) に分岐して BigQuery に書き込む

検証用の事前 cleanup やアサーション等は一切持たない。
"""

from __future__ import annotations

import csv
import datetime
import io
import json
import typing
from zoneinfo import ZoneInfo

import apache_beam as beam
from apache_beam.coders import RowCoder
from apache_beam.transforms.sql import SqlTransform


# ──────────────────────────────────────────────
# 入力スキーマ
# ──────────────────────────────────────────────


class InputRow(typing.NamedTuple):
    """全パイプライン共通の入力行スキーマ.

    event_at_utc は Calcite SQL の CAST AS TIMESTAMP で扱える形式
    ("YYYY-MM-DD HH:MM:SS") に正規化された文字列にしておく。
    NamedTuple に datetime を持たせると cross-language Sql の Row 変換で
    嵌まりやすかったため、文字列で運ぶ素直な実装にしている。
    """

    id: int
    name: typing.Optional[str]
    secret: str
    event_at_utc: str


beam.coders.registry.register_coder(InputRow, RowCoder)


# ──────────────────────────────────────────────
# BigQuery 出力スキーマ (3 種類)
# ──────────────────────────────────────────────

DROP_COL_SCHEMA = {
    "fields": [
        {"name": "id", "type": "INT64", "mode": "REQUIRED"},
        {"name": "name", "type": "STRING", "mode": "NULLABLE"},
        {"name": "event_at_utc", "type": "TIMESTAMP", "mode": "REQUIRED"},
    ]
}

UTC_JST_SCHEMA = {
    "fields": [
        {"name": "id", "type": "INT64", "mode": "REQUIRED"},
        {"name": "name", "type": "STRING", "mode": "NULLABLE"},
        {"name": "secret", "type": "STRING", "mode": "REQUIRED"},
        {"name": "event_at_jst", "type": "TIMESTAMP", "mode": "REQUIRED"},
    ]
}

NULL_DROP_SCHEMA = {
    "fields": [
        {"name": "id", "type": "INT64", "mode": "REQUIRED"},
        {"name": "name", "type": "STRING", "mode": "REQUIRED"},
        {"name": "secret", "type": "STRING", "mode": "REQUIRED"},
        {"name": "event_at_utc", "type": "TIMESTAMP", "mode": "REQUIRED"},
    ]
}


# ──────────────────────────────────────────────
# パース関数 (フォーマットごと)
# ──────────────────────────────────────────────


def parse_delimited_line(line: str, delimiter: str) -> dict:
    """CSV / TSV の 1 行を dict に変換する."""
    reader = csv.reader(io.StringIO(line), delimiter=delimiter)
    row = next(reader)
    return {
        "id": int(row[0]),
        "name": row[1] if row[1] != "" else None,
        "secret": row[2],
        "event_at_utc": row[3],
    }


def parse_json_line(line: str) -> dict:
    """NDJSON の 1 行を dict に変換する."""
    obj = json.loads(line)
    return {
        "id": int(obj["id"]),
        "name": obj.get("name"),
        "secret": obj["secret"],
        "event_at_utc": obj["event_at_utc"],
    }


def to_input_row(d: dict) -> InputRow:
    return InputRow(
        id=d["id"],
        name=d["name"],
        secret=d["secret"],
        event_at_utc=d["event_at_utc"],
    )


# ──────────────────────────────────────────────
# Python ブランチ (DoFn)
# ──────────────────────────────────────────────


class PyDropCol(beam.DoFn):
    """secret カラムを削除する."""

    def process(self, row: InputRow):
        yield {
            "id": row.id,
            "name": row.name,
            "event_at_utc": row.event_at_utc,
        }


class PyUtcToJst(beam.DoFn):
    """event_at_utc を JST に変換し event_at_jst として出力する."""

    def process(self, row: InputRow):
        utc = datetime.datetime.strptime(
            row.event_at_utc, "%Y-%m-%d %H:%M:%S"
        ).replace(tzinfo=ZoneInfo("UTC"))
        jst = utc.astimezone(ZoneInfo("Asia/Tokyo"))
        yield {
            "id": row.id,
            "name": row.name,
            "secret": row.secret,
            # BigQuery TIMESTAMP は ISO 8601 文字列を受け付ける
            "event_at_jst": jst.strftime("%Y-%m-%d %H:%M:%S"),
        }


class PyNullDrop(beam.DoFn):
    """name が NULL の行を除去する."""

    def process(self, row: InputRow):
        if row.name is None or row.name == "":
            return
        yield {
            "id": row.id,
            "name": row.name,
            "secret": row.secret,
            "event_at_utc": row.event_at_utc,
        }


# ──────────────────────────────────────────────
# SQL ブランチ (Calcite SQL)
# ──────────────────────────────────────────────

# 検証ポイント:
#   - SqlTransform は cross-language で Java 上の Calcite SQL を実行する
#   - PCOLLECTION は入力 PCollection を指す予約語
#   - JST 変換は CONVERT_TIMEZONE 等のランナー依存関数を避け、
#     "JST = UTC + 9h" の常時オフセットで TIMESTAMPADD で実装する

SQL_DROP_COL = """
SELECT id, name, event_at_utc
FROM PCOLLECTION
"""

SQL_UTC_JST = """
SELECT
  id,
  name,
  secret,
  TIMESTAMPADD(HOUR, 9, CAST(event_at_utc AS TIMESTAMP)) AS event_at_jst
FROM PCOLLECTION
"""

SQL_NULL_DROP = """
SELECT id, name, secret, event_at_utc
FROM PCOLLECTION
WHERE name IS NOT NULL
"""


def _sql_drop_col_to_dict(row) -> dict:
    return {
        "id": row.id,
        "name": row.name,
        "event_at_utc": row.event_at_utc,
    }


def _sql_utc_jst_to_dict(row) -> dict:
    ts = row.event_at_jst
    if hasattr(ts, "strftime"):
        ts_str = ts.strftime("%Y-%m-%d %H:%M:%S")
    else:
        ts_str = str(ts)
    return {
        "id": row.id,
        "name": row.name,
        "secret": row.secret,
        "event_at_jst": ts_str,
    }


def _sql_null_drop_to_dict(row) -> dict:
    return {
        "id": row.id,
        "name": row.name,
        "secret": row.secret,
        "event_at_utc": row.event_at_utc,
    }


# ──────────────────────────────────────────────
# パイプライン本体: 6 ブランチに分岐して BQ に書き込む
# ──────────────────────────────────────────────


def _bq_table(project: str, dataset: str, table: str) -> str:
    return f"{project}:{dataset}.{table}"


def build_branches(
    typed_rows: beam.PCollection,
    output_table_prefix: str,
    project: str,
    dataset: str,
) -> None:
    """typed_rows (InputRow) を 6 ブランチに分岐し BigQuery に書き込む.

    宛先テーブルは Terraform が事前に作成している前提のため
    create_disposition=CREATE_NEVER を指定する。
    """

    # ── SQL ブランチ ──────────────────────────────
    _ = (
        typed_rows
        | "SQL_DropCol" >> SqlTransform(SQL_DROP_COL)
        | "SQL_DropCol_ToDict" >> beam.Map(_sql_drop_col_to_dict)
        | "SQL_DropCol_Write"
        >> beam.io.WriteToBigQuery(
            table=_bq_table(project, dataset, f"{output_table_prefix}_sql_drop_col"),
            schema=DROP_COL_SCHEMA,
            create_disposition=beam.io.BigQueryDisposition.CREATE_NEVER,
            write_disposition=beam.io.BigQueryDisposition.WRITE_APPEND,
        )
    )

    _ = (
        typed_rows
        | "SQL_UtcJst" >> SqlTransform(SQL_UTC_JST)
        | "SQL_UtcJst_ToDict" >> beam.Map(_sql_utc_jst_to_dict)
        | "SQL_UtcJst_Write"
        >> beam.io.WriteToBigQuery(
            table=_bq_table(project, dataset, f"{output_table_prefix}_sql_utc_jst"),
            schema=UTC_JST_SCHEMA,
            create_disposition=beam.io.BigQueryDisposition.CREATE_NEVER,
            write_disposition=beam.io.BigQueryDisposition.WRITE_APPEND,
        )
    )

    _ = (
        typed_rows
        | "SQL_NullDrop" >> SqlTransform(SQL_NULL_DROP)
        | "SQL_NullDrop_ToDict" >> beam.Map(_sql_null_drop_to_dict)
        | "SQL_NullDrop_Write"
        >> beam.io.WriteToBigQuery(
            table=_bq_table(project, dataset, f"{output_table_prefix}_sql_null_drop"),
            schema=NULL_DROP_SCHEMA,
            create_disposition=beam.io.BigQueryDisposition.CREATE_NEVER,
            write_disposition=beam.io.BigQueryDisposition.WRITE_APPEND,
        )
    )

    # ── Python ブランチ ───────────────────────────
    _ = (
        typed_rows
        | "PY_DropCol" >> beam.ParDo(PyDropCol())
        | "PY_DropCol_Write"
        >> beam.io.WriteToBigQuery(
            table=_bq_table(project, dataset, f"{output_table_prefix}_py_drop_col"),
            schema=DROP_COL_SCHEMA,
            create_disposition=beam.io.BigQueryDisposition.CREATE_NEVER,
            write_disposition=beam.io.BigQueryDisposition.WRITE_APPEND,
        )
    )

    _ = (
        typed_rows
        | "PY_UtcJst" >> beam.ParDo(PyUtcToJst())
        | "PY_UtcJst_Write"
        >> beam.io.WriteToBigQuery(
            table=_bq_table(project, dataset, f"{output_table_prefix}_py_utc_jst"),
            schema=UTC_JST_SCHEMA,
            create_disposition=beam.io.BigQueryDisposition.CREATE_NEVER,
            write_disposition=beam.io.BigQueryDisposition.WRITE_APPEND,
        )
    )

    _ = (
        typed_rows
        | "PY_NullDrop" >> beam.ParDo(PyNullDrop())
        | "PY_NullDrop_Write"
        >> beam.io.WriteToBigQuery(
            table=_bq_table(project, dataset, f"{output_table_prefix}_py_null_drop"),
            schema=NULL_DROP_SCHEMA,
            create_disposition=beam.io.BigQueryDisposition.CREATE_NEVER,
            write_disposition=beam.io.BigQueryDisposition.WRITE_APPEND,
        )
    )
