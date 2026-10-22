"""Database connections. Credentials never appear in protocol messages or output."""

from __future__ import annotations

from typing import Any
from urllib.parse import urlsplit


class Connection:
    """Identifies a database.

    The engine reads credentials from environment variables of its process.
    Literal credentials (``Connection.postgresql(url=...)``) are handed to the
    engine through private environment variables. ``repr()`` never shows them.
    """

    __slots__ = ("_database", "_password", "_password_env", "_uri", "_url", "_url_env", "_user", "_user_env", "product")

    def __init__(
        self,
        product: str,
        *,
        url_env: str | None = None,
        url: str | None = None,
        uri: str | None = None,
        user_env: str | None = None,
        password_env: str | None = None,
        user: str | None = None,
        password: str | None = None,
        database: str | None = None,
    ) -> None:
        self.product = product
        self._url_env = url_env
        self._url = url
        self._uri = uri
        self._user_env = user_env
        self._password_env = password_env
        self._user = user
        self._password = password
        self._database = database

    @classmethod
    def postgresql(cls, *, url: str | None = None, url_env: str | None = None) -> Connection:
        """A PostgreSQL connection from a ``postgres://`` URL or a variable holding it."""
        if (url is None) == (url_env is None):
            raise ValueError("pass exactly one of url and url_env")
        if url is not None and not url.startswith(("postgres://", "postgresql://")):
            raise ValueError("a PostgreSQL URL must start with postgres:// or postgresql://")
        return cls("postgresql", url=url, url_env=url_env)

    @classmethod
    def neo4j(
        cls,
        uri: str | None = None,
        *,
        auth_env: str | None = None,
        url_env: str | None = None,
        user: str | None = None,
        password: str | None = None,
        database: str | None = None,
    ) -> Connection:
        """A Neo4j connection.

        Args:
            uri: ``neo4j://`` or ``bolt://`` URI without credentials.
            auth_env: Prefix of ``{auth_env}_USER`` and ``{auth_env}_PASSWORD``.
            url_env: Alternatively, a variable holding the full URL, which may
                contain ``user:password@``.
            user: Literal user name (prefer ``auth_env``).
            password: Literal password (prefer ``auth_env``).
            database: Database name, if not the server default.
        """
        if (uri is None) == (url_env is None):
            raise ValueError("pass exactly one of uri and url_env")
        if url_env is not None and (auth_env or user or password):
            raise ValueError("with url_env, put the credentials in the URL")
        if auth_env is not None and (user or password):
            raise ValueError("pass either auth_env or user/password, not both")
        if uri is not None and (urlsplit(uri).username or urlsplit(uri).password):
            raise ValueError("the URI must not contain credentials; use auth_env or user/password")
        return cls(
            "neo4j",
            url_env=url_env,
            uri=uri,
            user_env=f"{auth_env}_USER" if auth_env else None,
            password_env=f"{auth_env}_PASSWORD" if auth_env else None,
            user=user,
            password=password,
            database=database,
        )

    def __repr__(self) -> str:
        return f"<{self.product} connection>"

    __str__ = __repr__

    def _to_wire(self, env: dict[str, str] | None) -> dict[str, Any]:
        """Protocol form. Literal secrets go into ``env`` under private names."""

        def secret(value: str) -> str:
            if env is None:
                raise RuntimeError(
                    "connections with literal credentials can only be used through Migration; "
                    "use url_env or auth_env with Engine methods"
                )
            name = f"RTOGTOR_SDK_SECRET_{len(env)}"
            env[name] = value
            return name

        wire: dict[str, Any] = {"product": self.product}
        if self._url_env is not None or self._url is not None:
            wire["urlEnv"] = self._url_env if self._url_env is not None else secret(self._url or "")
        else:
            wire["uri"] = self._uri
            user_env = self._user_env or (secret(self._user) if self._user is not None else None)
            password_env = self._password_env or (secret(self._password) if self._password is not None else None)
            if user_env:
                wire["userEnv"] = user_env
            if password_env:
                wire["passwordEnv"] = password_env
        if self._database:
            wire["database"] = self._database
        return wire
