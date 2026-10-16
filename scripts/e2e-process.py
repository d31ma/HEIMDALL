import datetime
import json
import os
import pathlib
import signal
import subprocess
import sys
import time


def process_table():
    result = {}
    rows = subprocess.check_output(['ps', '-axo', 'pid=,ppid=,pgid=,uid=,lstart=,stat=,comm='], text=True)
    for line in rows.splitlines():
        fields = line.split(None, 10)
        if len(fields) == 11:
            pid, parent, group, uid = map(int, fields[:4])
            result[pid] = {'pid': pid, 'parent': parent, 'group': group, 'uid': uid,
                           'birth': ' '.join(fields[4:9]), 'state': fields[9], 'command': fields[10]}
    return result


def identity(row):
    return row['uid'], row['birth'], row['command']


def current_owned(known):
    table = process_table()
    return {pid: row for pid, row in table.items()
            if pid in known and identity(row) == identity(known[pid]) and not row['state'].startswith('Z')}


def capture(process, known):
    table = process_table()
    leader = table.get(process.pid)
    original = known.get(process.pid)
    if process.poll() is None and leader is not None and (original is None or identity(leader) == identity(original)):
        known.update({pid: row for pid, row in table.items()
                      if row['group'] == process.pid and row['uid'] == os.getuid()})


def send_owned(known, signals):
    for pid in current_owned(known):
        for sig in signals:
            try:
                os.kill(pid, sig)
            except ProcessLookupError:
                pass


def stop_owned(process, known):
    capture(process, known)
    leader = current_owned(known).get(process.pid)
    if process.poll() is None and leader is not None:
        send_owned({process.pid: leader}, [signal.SIGCONT, signal.SIGTERM])
        try:
            process.wait(timeout=20)
        except subprocess.TimeoutExpired:
            pass
    send_owned(known, [signal.SIGCONT, signal.SIGTERM])
    deadline = time.monotonic() + 5
    while current_owned(known) and time.monotonic() < deadline:
        time.sleep(.1)
    send_owned(known, [signal.SIGKILL])
    if process.poll() is None:
        try:
            process.wait(timeout=2)
        except subprocess.TimeoutExpired:
            pass
    return sorted(current_owned(known))


def supervise(mode, log, receipt, label, seconds, pidfile, command):
    started = time.monotonic()
    outcome = 'interrupted'
    known = {}
    result = 143
    def interrupted(_signal, _frame):
        raise InterruptedError()
    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGINT, interrupted)
    with open(log, 'wb') as output:
        process = subprocess.Popen(command, stdout=output, stderr=subprocess.STDOUT, start_new_session=True)
        if pidfile:
            pathlib.Path(pidfile).write_text(str(process.pid))
        capture(process, known)
        if pidfile and process.pid in known:
            pathlib.Path(pidfile + '.identity.json').write_text(json.dumps(known[process.pid]))
        try:
            while process.poll() is None:
                capture(process, known)
                if seconds and time.monotonic() - started >= seconds:
                    outcome = 'timed_out'
                    result = 124
                    break
                time.sleep(.1)
            else:
                result = process.returncode
                outcome = 'completed'
        except InterruptedError:
            pass
        finally:
            signal.signal(signal.SIGTERM, signal.SIG_IGN)
            signal.signal(signal.SIGINT, signal.SIG_IGN)
            remaining = stop_owned(process, known)
            record = {'label': label, 'pid': process.pid, 'supervisor_pid': os.getpid(),
                      'executable': pathlib.Path(command[0]).name, 'exit_code': result,
                      'outcome': outcome, 'elapsed_seconds': round(time.monotonic() - started, 3),
                      'finished_at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
                      'recorded_family_pids': sorted(known), 'remaining_owned_pids': remaining,
                      'cleanup_complete': not remaining, 'process_groups_signalled': False}
            with open(receipt, 'a') as records:
                records.write(json.dumps(record) + '\n')
            if remaining:
                result = 1
    return result


if __name__ == '__main__':
    mode, log, receipt, label, seconds, pidfile = sys.argv[1:7]
    raise SystemExit(supervise(mode, log, receipt, label, int(seconds), pidfile, sys.argv[7:]))
