"""Search backends for the eval harness: in-process Python, or a live Go shard.

Both return the same shape, so run_eval scores either identically. Dense
track ids are positions in the manifest's sorted "indexed" list, which is
how both prototype/build_index.py and cmd/ingest assign them.
"""

import sys
from dataclasses import dataclass
from pathlib import Path

import numpy as np

from prototype.index import Index
from prototype.match import search

ROOT = Path(__file__).resolve().parent.parent
# Generated gRPC stubs (buf.gen.yaml) live outside the package tree.
sys.path.insert(0, str(ROOT / "gen" / "python"))

# Top-k requested from a shard: must exceed the largest duplicate cluster so
# the runner-up outside the top-1's cluster is in the list (D14, D22).
SHARD_TOP_K = 20


@dataclass(frozen=True)
class Hit:
    top1_dense: int  # -1 if no candidate
    top1_score: int
    top1_offset: int
    runner_up: int  # best score outside the top-1's duplicate cluster
    postings_scanned: int
    scale: float = 0.0  # triplet index: fitted tempo factor; 0 for pairs


class LocalBackend:
    def __init__(self, index_path: Path) -> None:
        self.index = Index.load(index_path)

    def search(self, hashes: np.ndarray, times: np.ndarray) -> Hit:
        res = search(self.index, hashes, times, top_k=1)
        if not res.candidates:
            return Hit(-1, 0, 0, 0, res.postings_scanned)
        c = res.candidates[0]
        return Hit(c.track, c.score, c.offset, res.runner_up, res.postings_scanned)


class ShardBackend:
    """Calls ShardService.Search on a running cmd/shard over gRPC."""

    def __init__(self, addr: str, dense_cluster: np.ndarray, timeout_s: float = 5.0) -> None:
        import grpc
        from landmark.v1 import landmark_pb2, landmark_pb2_grpc

        self._pb = landmark_pb2
        self._channel = grpc.insecure_channel(addr)
        self._stub = landmark_pb2_grpc.ShardServiceStub(self._channel)
        self._cluster = dense_cluster
        self._timeout = timeout_s
        self._grpc = grpc
        self.retries = 0  # transient failures retried; recorded in run metadata

    def search(
        self,
        hashes: np.ndarray,
        times: np.ndarray,
        spans: np.ndarray | None = None,
        bands: np.ndarray | None = None,
    ) -> Hit:
        """Pair landmarks, or with spans and bands, triplet landmarks (for a
        shard serving a triplet index)."""
        if len(hashes) == 0:
            # Silent excerpts fingerprint to nothing. The shard rejects empty
            # queries as INVALID_ARGUMENT, so answer "no match" locally, as the
            # Python matcher does.
            return Hit(-1, 0, 0, 0, 0)
        req = self._pb.SearchRequest(
            top_k=SHARD_TOP_K, hashes=hashes.tolist(), times=times.tolist()
        )
        if spans is not None and bands is not None:
            req.spans = np.asarray(spans, np.uint8).tobytes()
            req.bands = np.asarray(bands, np.uint8).tobytes()
        resp = self._search_with_retry(req)
        cands = resp.candidates
        if not cands:
            return Hit(-1, 0, 0, 0, resp.postings_scanned)
        top = cands[0]
        top_cluster = self._cluster[top.track_id]
        runner = next((c.score for c in cands[1:] if self._cluster[c.track_id] != top_cluster), 0)
        return Hit(top.track_id, top.score, top.offset, runner, resp.postings_scanned, top.scale)

    def _search_with_retry(self, req, attempts: int = 3):  # noqa: ANN001, ANN202
        """Retry timeouts and unavailability: an eval run should not die on one
        transient failure (e.g. the machine sleeping mid-call). Other errors,
        and a third consecutive failure, propagate."""
        transient = {self._grpc.StatusCode.DEADLINE_EXCEEDED, self._grpc.StatusCode.UNAVAILABLE}
        for i in range(attempts):
            try:
                return self._stub.Search(req, timeout=self._timeout)
            except self._grpc.RpcError as e:
                if e.code() not in transient or i == attempts - 1:
                    raise
                self.retries += 1
        raise AssertionError("unreachable")

    def close(self) -> None:
        self._channel.close()


def dense_tables(manifest: dict) -> tuple[np.ndarray, np.ndarray]:
    """(dense id -> FMA id, dense id -> dense cluster representative)."""
    fma = np.array(sorted(manifest["indexed"]), dtype=np.int64)
    dense = {int(f): i for i, f in enumerate(fma)}
    cluster = np.arange(len(fma), dtype=np.int64)
    for members in manifest["clusters"].values():
        ds = [dense[f] for f in members if f in dense]
        if ds:
            cluster[ds] = min(ds)
    return fma, cluster
