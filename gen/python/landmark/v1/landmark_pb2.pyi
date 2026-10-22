from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class Candidate(_message.Message):
    __slots__ = ("track_id", "score", "offset", "scale")
    TRACK_ID_FIELD_NUMBER: _ClassVar[int]
    SCORE_FIELD_NUMBER: _ClassVar[int]
    OFFSET_FIELD_NUMBER: _ClassVar[int]
    SCALE_FIELD_NUMBER: _ClassVar[int]
    track_id: int
    score: int
    offset: int
    scale: float
    def __init__(self, track_id: _Optional[int] = ..., score: _Optional[int] = ..., offset: _Optional[int] = ..., scale: _Optional[float] = ...) -> None: ...

class SearchRequest(_message.Message):
    __slots__ = ("top_k", "hashes", "times", "spans", "bands", "manifest_version")
    TOP_K_FIELD_NUMBER: _ClassVar[int]
    HASHES_FIELD_NUMBER: _ClassVar[int]
    TIMES_FIELD_NUMBER: _ClassVar[int]
    SPANS_FIELD_NUMBER: _ClassVar[int]
    BANDS_FIELD_NUMBER: _ClassVar[int]
    MANIFEST_VERSION_FIELD_NUMBER: _ClassVar[int]
    top_k: int
    hashes: _containers.RepeatedScalarFieldContainer[int]
    times: _containers.RepeatedScalarFieldContainer[int]
    spans: bytes
    bands: bytes
    manifest_version: int
    def __init__(self, top_k: _Optional[int] = ..., hashes: _Optional[_Iterable[int]] = ..., times: _Optional[_Iterable[int]] = ..., spans: _Optional[bytes] = ..., bands: _Optional[bytes] = ..., manifest_version: _Optional[int] = ...) -> None: ...

class SearchResponse(_message.Message):
    __slots__ = ("candidates", "partition_id", "postings_scanned", "manifest_version")
    CANDIDATES_FIELD_NUMBER: _ClassVar[int]
    PARTITION_ID_FIELD_NUMBER: _ClassVar[int]
    POSTINGS_SCANNED_FIELD_NUMBER: _ClassVar[int]
    MANIFEST_VERSION_FIELD_NUMBER: _ClassVar[int]
    candidates: _containers.RepeatedCompositeFieldContainer[Candidate]
    partition_id: int
    postings_scanned: int
    manifest_version: int
    def __init__(self, candidates: _Optional[_Iterable[_Union[Candidate, _Mapping]]] = ..., partition_id: _Optional[int] = ..., postings_scanned: _Optional[int] = ..., manifest_version: _Optional[int] = ...) -> None: ...

class ServedVersion(_message.Message):
    __slots__ = ("version", "checksum")
    VERSION_FIELD_NUMBER: _ClassVar[int]
    CHECKSUM_FIELD_NUMBER: _ClassVar[int]
    version: int
    checksum: int
    def __init__(self, version: _Optional[int] = ..., checksum: _Optional[int] = ...) -> None: ...

class InfoRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class InfoResponse(_message.Message):
    __slots__ = ("partition_id", "n_partitions", "n_tracks", "n_keys", "n_postings", "segment_crc32c", "format_version", "versions")
    PARTITION_ID_FIELD_NUMBER: _ClassVar[int]
    N_PARTITIONS_FIELD_NUMBER: _ClassVar[int]
    N_TRACKS_FIELD_NUMBER: _ClassVar[int]
    N_KEYS_FIELD_NUMBER: _ClassVar[int]
    N_POSTINGS_FIELD_NUMBER: _ClassVar[int]
    SEGMENT_CRC32C_FIELD_NUMBER: _ClassVar[int]
    FORMAT_VERSION_FIELD_NUMBER: _ClassVar[int]
    VERSIONS_FIELD_NUMBER: _ClassVar[int]
    partition_id: int
    n_partitions: int
    n_tracks: int
    n_keys: int
    n_postings: int
    segment_crc32c: int
    format_version: int
    versions: _containers.RepeatedCompositeFieldContainer[ServedVersion]
    def __init__(self, partition_id: _Optional[int] = ..., n_partitions: _Optional[int] = ..., n_tracks: _Optional[int] = ..., n_keys: _Optional[int] = ..., n_postings: _Optional[int] = ..., segment_crc32c: _Optional[int] = ..., format_version: _Optional[int] = ..., versions: _Optional[_Iterable[_Union[ServedVersion, _Mapping]]] = ...) -> None: ...

class IdentifyRequest(_message.Message):
    __slots__ = ("top_k", "hashes", "times", "spans", "bands")
    TOP_K_FIELD_NUMBER: _ClassVar[int]
    HASHES_FIELD_NUMBER: _ClassVar[int]
    TIMES_FIELD_NUMBER: _ClassVar[int]
    SPANS_FIELD_NUMBER: _ClassVar[int]
    BANDS_FIELD_NUMBER: _ClassVar[int]
    top_k: int
    hashes: _containers.RepeatedScalarFieldContainer[int]
    times: _containers.RepeatedScalarFieldContainer[int]
    spans: bytes
    bands: bytes
    def __init__(self, top_k: _Optional[int] = ..., hashes: _Optional[_Iterable[int]] = ..., times: _Optional[_Iterable[int]] = ..., spans: _Optional[bytes] = ..., bands: _Optional[bytes] = ...) -> None: ...

class IdentifyResponse(_message.Message):
    __slots__ = ("candidates", "matched", "partial", "partitions_responded", "partitions_total", "index_version")
    CANDIDATES_FIELD_NUMBER: _ClassVar[int]
    MATCHED_FIELD_NUMBER: _ClassVar[int]
    PARTIAL_FIELD_NUMBER: _ClassVar[int]
    PARTITIONS_RESPONDED_FIELD_NUMBER: _ClassVar[int]
    PARTITIONS_TOTAL_FIELD_NUMBER: _ClassVar[int]
    INDEX_VERSION_FIELD_NUMBER: _ClassVar[int]
    candidates: _containers.RepeatedCompositeFieldContainer[Candidate]
    matched: bool
    partial: bool
    partitions_responded: int
    partitions_total: int
    index_version: int
    def __init__(self, candidates: _Optional[_Iterable[_Union[Candidate, _Mapping]]] = ..., matched: _Optional[bool] = ..., partial: _Optional[bool] = ..., partitions_responded: _Optional[int] = ..., partitions_total: _Optional[int] = ..., index_version: _Optional[int] = ...) -> None: ...
