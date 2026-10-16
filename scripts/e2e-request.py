import datetime
import json
import pathlib
import subprocess
import sys
import time
import urllib.parse


def append(path, record):
    with path.open('a') as output:
        output.write(json.dumps(record) + '\n')


def request(receipt, response, arguments):
    receipt = pathlib.Path(receipt)
    response = pathlib.Path(response)
    response.unlink(missing_ok=True)
    method = 'GET'
    for index, value in enumerate(arguments[:-1]):
        if value == '-X':
            method = arguments[index + 1]
    route = urllib.parse.urlsplit(arguments[-1]).path
    started = time.monotonic()
    record = {'method': method, 'path': route,
              'started_utc': datetime.datetime.now(datetime.timezone.utc).isoformat()}
    append(receipt, dict(record, phase='started'))
    metadata = '%{http_code} %{http_version} %{size_upload} %{size_download} %{time_total}'
    try:
        process = subprocess.run(['curl', '--output', str(response), '--write-out', metadata, *arguments],
                                 stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=35)
    except subprocess.TimeoutExpired:
        append(receipt, dict(record, phase='finished', curl_exit_code=124,
                             elapsed_seconds=round(time.monotonic() - started, 3)))
        return 124
    values = process.stdout.decode(errors='replace').split()
    record.update(phase='finished', curl_exit_code=process.returncode,
                  elapsed_seconds=round(time.monotonic() - started, 3),
                  response_bytes=response.stat().st_size if response.exists() else 0)
    if len(values) == 5:
        record.update(http_status=values[0], http_version=values[1],
                      uploaded_bytes=values[2], downloaded_bytes=values[3], transport_seconds=values[4])
    append(receipt, record)
    if process.returncode == 0 and response.exists():
        sys.stdout.buffer.write(response.read_bytes())
    return process.returncode


if __name__ == '__main__':
    raise SystemExit(request(sys.argv[1], sys.argv[2], sys.argv[3:]))
