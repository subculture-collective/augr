"""Normal operator login/renewal; secret values never enter command arguments."""
import datetime as dt
import getpass
import json
import os
from pathlib import Path
import stat
import tempfile
import urllib.error
import urllib.request

from .core import UTC, Refusal, instant, lock, require


def auth_request(api_url, endpoint, payload):
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, req, fp, code, msg, headers, newurl):
            return None
    request = urllib.request.Request(api_url + '/api/v1/auth/' + endpoint,
                                     data=json.dumps(payload).encode(),
                                     headers={'Content-Type': 'application/json'}, method='POST')
    try:
        with urllib.request.build_opener(NoRedirect).open(request, timeout=10) as response:
            data = json.loads(response.read(65536))
        require(all(isinstance(data.get(k), str) and data[k] for k in
                    ('access_token', 'refresh_token', 'expires_at')), 'invalid_session_response')
        require(instant(data['expires_at']) > dt.datetime.now(UTC), 'expired_session_response')
        return {k: data[k] for k in ('access_token', 'refresh_token', 'expires_at')}
    except (OSError, ValueError, urllib.error.URLError):
        raise Refusal('normal_login_or_refresh_failed') from None


def private_file(path):
    require(not path.is_symlink(), 'session_symlink')
    info = path.stat()
    require(stat.S_ISREG(info.st_mode) and info.st_mode & 0o077 == 0
            and info.st_uid == os.getuid(), 'session_file_permissions')


def store_session(path, data):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    if path.exists() or path.is_symlink():
        private_file(path)
    fd, name = tempfile.mkstemp(prefix='.' + path.name + '-', dir=path.parent)
    try:
        with os.fdopen(fd, 'w') as stream:
            json.dump(data, stream)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(name, path)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def login(api_url, path):
    require(Path(path).is_absolute(), 'session_path_must_be_absolute')
    print('Normal Augr login at ' + api_url + '; session destination: ' + str(path))
    username = input('Operator username/email: ').strip()
    password = getpass.getpass('Password (hidden): ')
    require(username and password, 'login_values_required')
    # Check destination before making the authenticated request.
    if Path(path).exists() or Path(path).is_symlink():
        private_file(Path(path))
    with lock(str(path) + '.lock'):
        session = auth_request(api_url, 'login', {'username': username, 'password': password})
        store_session(path, session)
    return {'session_file': str(path), 'expires_at': session['expires_at']}


def access_token(path, api_url, now=None):
    path = Path(path)
    private_file(path)
    with lock(str(path) + '.lock'):
        raw = path.read_text().strip()
        # Existing access-only files still work, but cannot silently be renewed.
        if not raw.startswith('{'):
            require(raw and '\n' not in raw, 'invalid_token_file')
            return raw
        data = json.loads(raw)
        if (instant(data['expires_at']) - (now or dt.datetime.now(UTC))).total_seconds() <= 60:
            require(bool(data.get('refresh_token')), 'session_renewal_required')
            data = auth_request(api_url, 'refresh', {'refresh_token': data['refresh_token']})
            store_session(path, data)
        return data['access_token']
