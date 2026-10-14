from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class Landmark(_message.Message):
    __slots__ = ("hash", "t")
    HASH_FIELD_NUMBER: _ClassVar[int]
    T_FIELD_NUMBER: _ClassVar[int]
    hash: int
    t: int
    def __init__(self, hash: _Optional[int] = ..., t: _Optional[int] = ...) -> None: ...

class Candidate(_message.Message):
    __slots__ = ("track_id", "score", "offset")
    TRACK_ID_FIELD_NUMBER: _ClassVar[int]
    SCORE_FIELD_NUMBER: _ClassVar[int]
    OFFSET_FIELD_NUMBER: _ClassVar[int]
    track_id: int
    score: int
    offset: int
    def __init__(self, track_id: _Optional[int] = ..., score: _Optional[int] = ..., offset: _Optional[int] = ...) -> None: ...

class SearchRequest(_message.Message):
    __slots__ = ("landmarks", "top_k")
    LANDMARKS_FIELD_NUMBER: _ClassVar[int]
    TOP_K_FIELD_NUMBER: _ClassVar[int]
    landmarks: _containers.RepeatedCompositeFieldContainer[Landmark]
    top_k: int
    def __init__(self, landmarks: _Optional[_Iterable[_Union[Landmark, _Mapping]]] = ..., top_k: _Optional[int] = ...) -> None: ...

class SearchResponse(_message.Message):
    __slots__ = ("candidates", "partition_id", "postings_scanned")
    CANDIDATES_FIELD_NUMBER: _ClassVar[int]
    PARTITION_ID_FIELD_NUMBER: _ClassVar[int]
    POSTINGS_SCANNED_FIELD_NUMBER: _ClassVar[int]
    candidates: _containers.RepeatedCompositeFieldContainer[Candidate]
    partition_id: int
    postings_scanned: int
    def __init__(self, candidates: _Optional[_Iterable[_Union[Candidate, _Mapping]]] = ..., partition_id: _Optional[int] = ..., postings_scanned: _Optional[int] = ...) -> None: ...

class InfoRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class InfoResponse(_message.Message):
    __slots__ = ("partition_id", "n_partitions", "n_tracks", "n_keys", "n_postings", "segment_crc32c", "format_version")
    PARTITION_ID_FIELD_NUMBER: _ClassVar[int]
    N_PARTITIONS_FIELD_NUMBER: _ClassVar[int]
    N_TRACKS_FIELD_NUMBER: _ClassVar[int]
    N_KEYS_FIELD_NUMBER: _ClassVar[int]
    N_POSTINGS_FIELD_NUMBER: _ClassVar[int]
    SEGMENT_CRC32C_FIELD_NUMBER: _ClassVar[int]
    FORMAT_VERSION_FIELD_NUMBER: _ClassVar[int]
    partition_id: int
    n_partitions: int
    n_tracks: int
    n_keys: int
    n_postings: int
    segment_crc32c: int
    format_version: int
    def __init__(self, partition_id: _Optional[int] = ..., n_partitions: _Optional[int] = ..., n_tracks: _Optional[int] = ..., n_keys: _Optional[int] = ..., n_postings: _Optional[int] = ..., segment_crc32c: _Optional[int] = ..., format_version: _Optional[int] = ...) -> None: ...

class IdentifyRequest(_message.Message):
    __slots__ = ("landmarks", "top_k")
    LANDMARKS_FIELD_NUMBER: _ClassVar[int]
    TOP_K_FIELD_NUMBER: _ClassVar[int]
    landmarks: _containers.RepeatedCompositeFieldContainer[Landmark]
    top_k: int
    def __init__(self, landmarks: _Optional[_Iterable[_Union[Landmark, _Mapping]]] = ..., top_k: _Optional[int] = ...) -> None: ...

class IdentifyResponse(_message.Message):
    __slots__ = ("candidates", "matched", "partial", "partitions_responded", "partitions_total")
    CANDIDATES_FIELD_NUMBER: _ClassVar[int]
    MATCHED_FIELD_NUMBER: _ClassVar[int]
    PARTIAL_FIELD_NUMBER: _ClassVar[int]
    PARTITIONS_RESPONDED_FIELD_NUMBER: _ClassVar[int]
    PARTITIONS_TOTAL_FIELD_NUMBER: _ClassVar[int]
    candidates: _containers.RepeatedCompositeFieldContainer[Candidate]
    matched: bool
    partial: bool
    partitions_responded: int
    partitions_total: int
    def __init__(self, candidates: _Optional[_Iterable[_Union[Candidate, _Mapping]]] = ..., matched: _Optional[bool] = ..., partial: _Optional[bool] = ..., partitions_responded: _Optional[int] = ..., partitions_total: _Optional[int] = ...) -> None: ...
