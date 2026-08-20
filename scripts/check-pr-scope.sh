#!/bin/sh

set -eu

for changed_file do
	case "$changed_file" in
		packages/?*.json)
			package_file=${changed_file#packages/}
			case "$package_file" in
				*/*)
					printf 'external submissions may only change packages/*.json: %s\n' "$changed_file" >&2
					exit 1
					;;
			esac
			;;
		*)
			printf 'external submissions may only change packages/*.json: %s\n' "$changed_file" >&2
			exit 1
			;;
	esac
done
