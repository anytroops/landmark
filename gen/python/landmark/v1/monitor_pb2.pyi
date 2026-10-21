from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class EventKind(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    EVENT_KIND_UNSPECIFIED: _ClassVar[EventKind]
    EVENT_KIND_START: _ClassVar[EventKind]
    EVENT_KIND_END: _ClassVar[EventKind]
EVENT_KIND_UNSPECIFIED: EventKind
EVENT_KIND_START: EventKind
EVENT_KIND_END: EventKind

class Stream(_message.Message):
    __slots__ = ("id", "url")
    ID_FIELD_NUMBER: _ClassVar[int]
    URL_FIELD_NUMBER: _ClassVar[int]
    id: str
    url: str
    def __init__(self, id: _Optional[str] = ..., url: _Optional[str] = ...) -> None: ...

class Lease(_message.Message):
    __slots__ = ("stream_id", "url", "epoch", "next_seq", "open")
    STREAM_ID_FIELD_NUMBER: _ClassVar[int]
    URL_FIELD_NUMBER: _ClassVar[int]
    EPOCH_FIELD_NUMBER: _ClassVar[int]
    NEXT_SEQ_FIELD_NUMBER: _ClassVar[int]
    OPEN_FIELD_NUMBER: _ClassVar[int]
    stream_id: str
    url: str
    epoch: int
    next_seq: int
    open: Event
    def __init__(self, stream_id: _Optional[str] = ..., url: _Optional[str] = ..., epoch: _Optional[int] = ..., next_seq: _Optional[int] = ..., open: _Optional[_Union[Event, _Mapping]] = ...) -> None: ...

class HeldLease(_message.Message):
    __slots__ = ("stream_id", "epoch")
    STREAM_ID_FIELD_NUMBER: _ClassVar[int]
    EPOCH_FIELD_NUMBER: _ClassVar[int]
    stream_id: str
    epoch: int
    def __init__(self, stream_id: _Optional[str] = ..., epoch: _Optional[int] = ...) -> None: ...

class HeartbeatRequest(_message.Message):
    __slots__ = ("worker_id", "capacity", "held")
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    CAPACITY_FIELD_NUMBER: _ClassVar[int]
    HELD_FIELD_NUMBER: _ClassVar[int]
    worker_id: str
    capacity: int
    held: _containers.RepeatedCompositeFieldContainer[HeldLease]
    def __init__(self, worker_id: _Optional[str] = ..., capacity: _Optional[int] = ..., held: _Optional[_Iterable[_Union[HeldLease, _Mapping]]] = ...) -> None: ...

class HeartbeatResponse(_message.Message):
    __slots__ = ("leases", "ttl_ms")
    LEASES_FIELD_NUMBER: _ClassVar[int]
    TTL_MS_FIELD_NUMBER: _ClassVar[int]
    leases: _containers.RepeatedCompositeFieldContainer[Lease]
    ttl_ms: int
    def __init__(self, leases: _Optional[_Iterable[_Union[Lease, _Mapping]]] = ..., ttl_ms: _Optional[int] = ...) -> None: ...

class Event(_message.Message):
    __slots__ = ("stream_id", "seq", "epoch", "kind", "track_id", "name", "start_unix", "end_unix", "tempo", "evidence", "resumed", "interrupted", "worker_id", "decided_unix", "logged_unix", "source_start_unix")
    STREAM_ID_FIELD_NUMBER: _ClassVar[int]
    SEQ_FIELD_NUMBER: _ClassVar[int]
    EPOCH_FIELD_NUMBER: _ClassVar[int]
    KIND_FIELD_NUMBER: _ClassVar[int]
    TRACK_ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    START_UNIX_FIELD_NUMBER: _ClassVar[int]
    END_UNIX_FIELD_NUMBER: _ClassVar[int]
    TEMPO_FIELD_NUMBER: _ClassVar[int]
    EVIDENCE_FIELD_NUMBER: _ClassVar[int]
    RESUMED_FIELD_NUMBER: _ClassVar[int]
    INTERRUPTED_FIELD_NUMBER: _ClassVar[int]
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    DECIDED_UNIX_FIELD_NUMBER: _ClassVar[int]
    LOGGED_UNIX_FIELD_NUMBER: _ClassVar[int]
    SOURCE_START_UNIX_FIELD_NUMBER: _ClassVar[int]
    stream_id: str
    seq: int
    epoch: int
    kind: EventKind
    track_id: int
    name: str
    start_unix: float
    end_unix: float
    tempo: float
    evidence: float
    resumed: bool
    interrupted: bool
    worker_id: str
    decided_unix: float
    logged_unix: float
    source_start_unix: float
    def __init__(self, stream_id: _Optional[str] = ..., seq: _Optional[int] = ..., epoch: _Optional[int] = ..., kind: _Optional[_Union[EventKind, str]] = ..., track_id: _Optional[int] = ..., name: _Optional[str] = ..., start_unix: _Optional[float] = ..., end_unix: _Optional[float] = ..., tempo: _Optional[float] = ..., evidence: _Optional[float] = ..., resumed: _Optional[bool] = ..., interrupted: _Optional[bool] = ..., worker_id: _Optional[str] = ..., decided_unix: _Optional[float] = ..., logged_unix: _Optional[float] = ..., source_start_unix: _Optional[float] = ...) -> None: ...

class AppendRequest(_message.Message):
    __slots__ = ("stream_id", "epoch", "events")
    STREAM_ID_FIELD_NUMBER: _ClassVar[int]
    EPOCH_FIELD_NUMBER: _ClassVar[int]
    EVENTS_FIELD_NUMBER: _ClassVar[int]
    stream_id: str
    epoch: int
    events: _containers.RepeatedCompositeFieldContainer[Event]
    def __init__(self, stream_id: _Optional[str] = ..., epoch: _Optional[int] = ..., events: _Optional[_Iterable[_Union[Event, _Mapping]]] = ...) -> None: ...

class AppendResponse(_message.Message):
    __slots__ = ("next_seq",)
    NEXT_SEQ_FIELD_NUMBER: _ClassVar[int]
    next_seq: int
    def __init__(self, next_seq: _Optional[int] = ...) -> None: ...

class SubscribeRequest(_message.Message):
    __slots__ = ("stream_ids", "live_only")
    STREAM_IDS_FIELD_NUMBER: _ClassVar[int]
    LIVE_ONLY_FIELD_NUMBER: _ClassVar[int]
    stream_ids: _containers.RepeatedScalarFieldContainer[str]
    live_only: bool
    def __init__(self, stream_ids: _Optional[_Iterable[str]] = ..., live_only: _Optional[bool] = ...) -> None: ...

class SubscribeResponse(_message.Message):
    __slots__ = ("event",)
    EVENT_FIELD_NUMBER: _ClassVar[int]
    event: Event
    def __init__(self, event: _Optional[_Union[Event, _Mapping]] = ...) -> None: ...

class AddStreamRequest(_message.Message):
    __slots__ = ("stream",)
    STREAM_FIELD_NUMBER: _ClassVar[int]
    stream: Stream
    def __init__(self, stream: _Optional[_Union[Stream, _Mapping]] = ...) -> None: ...

class AddStreamResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ListStreamsRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class StreamStatus(_message.Message):
    __slots__ = ("stream", "holder", "epoch", "next_seq")
    STREAM_FIELD_NUMBER: _ClassVar[int]
    HOLDER_FIELD_NUMBER: _ClassVar[int]
    EPOCH_FIELD_NUMBER: _ClassVar[int]
    NEXT_SEQ_FIELD_NUMBER: _ClassVar[int]
    stream: Stream
    holder: str
    epoch: int
    next_seq: int
    def __init__(self, stream: _Optional[_Union[Stream, _Mapping]] = ..., holder: _Optional[str] = ..., epoch: _Optional[int] = ..., next_seq: _Optional[int] = ...) -> None: ...

class ListStreamsResponse(_message.Message):
    __slots__ = ("streams",)
    STREAMS_FIELD_NUMBER: _ClassVar[int]
    streams: _containers.RepeatedCompositeFieldContainer[StreamStatus]
    def __init__(self, streams: _Optional[_Iterable[_Union[StreamStatus, _Mapping]]] = ...) -> None: ...
