"""Dataflow Flex Template entrypoint.

ファイル形式 (--format) に応じた読み込みのみここで吸収し、
共通の変換ロジックは transforms.build_branches に委譲する。

ピュアな ETL に徹し、テスト用の cleanup / アサーション / 条件分岐は一切持たない。
"""

from __future__ import annotations

import argparse
import logging

import apache_beam as beam
from apache_beam.io.filesystem import CompressionTypes
from apache_beam.options.pipeline_options import PipelineOptions, SetupOptions

from transforms import (
    InputRow,
    build_branches,
    parse_delimited_line,
    parse_json_line,
    to_input_row,
)


def _parse_csv(line: str) -> dict:
    return parse_delimited_line(line, ",")


def _parse_tsv(line: str) -> dict:
    return parse_delimited_line(line, "\t")


def run(argv=None):
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--format",
        required=True,
        choices=["csv", "tsv", "json", "csv_gz"],
        help="入力ファイル形式",
    )
    parser.add_argument("--input_path", required=True, help="GCS の入力ファイルパス")
    parser.add_argument(
        "--output_table_prefix",
        required=True,
        help="BQ 宛先テーブルのプレフィックス (例: csv, tsv, json, csv_gz)",
    )
    parser.add_argument("--bq_project", required=True)
    parser.add_argument("--bq_dataset", required=True)
    args, pipeline_args = parser.parse_known_args(argv)

    options = PipelineOptions(pipeline_args)
    options.view_as(SetupOptions).save_main_session = True

    with beam.Pipeline(options=options) as p:
        if args.format == "csv":
            raw = p | "Read" >> beam.io.ReadFromText(
                args.input_path, skip_header_lines=1
            )
            parsed = raw | "Parse" >> beam.Map(_parse_csv)
        elif args.format == "tsv":
            raw = p | "Read" >> beam.io.ReadFromText(
                args.input_path, skip_header_lines=1
            )
            parsed = raw | "Parse" >> beam.Map(_parse_tsv)
        elif args.format == "json":
            raw = p | "Read" >> beam.io.ReadFromText(args.input_path)
            parsed = raw | "Parse" >> beam.Map(parse_json_line)
        elif args.format == "csv_gz":
            # ReadFromText は compression_type=AUTO がデフォルトで
            # .gz 拡張子から gzip を自動判別する
            raw = p | "Read" >> beam.io.ReadFromText(
                args.input_path,
                skip_header_lines=1,
                compression_type=CompressionTypes.AUTO,
            )
            parsed = raw | "Parse" >> beam.Map(_parse_csv)
        else:
            raise ValueError(f"unknown format: {args.format}")

        typed_rows = (
            parsed
            | "ToInputRow" >> beam.Map(to_input_row).with_output_types(InputRow)
        )

        build_branches(
            typed_rows=typed_rows,
            output_table_prefix=args.output_table_prefix,
            project=args.bq_project,
            dataset=args.bq_dataset,
        )


if __name__ == "__main__":
    logging.getLogger().setLevel(logging.INFO)
    run()
