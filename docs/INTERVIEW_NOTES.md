# Interview Notes

After each core component: 3 interview-style questions and Sean's answers.

## Fingerprinter + matcher (Python prototype), 2026-10-14

### Q1. Why score by the largest vote count at one time offset instead of total matching hashes?
**Sean:** Random hashes could match a completely unrelated song just by chance.

**Feedback:** Right instinct; missing the mechanism. Chance collisions land at random offsets and spread out; a true match lines up at one fixed offset because the query is the song shifted in time. Total count also favors long or busy tracks.

**Strong answer:** "A true match isn't just shared hashes, it's shared hashes that line up in time. Each hit votes for a time offset, and only the correct song produces a spike at one offset. Chance collisions spread across offsets. On fma_small val negatives (~11k queries) the max wrong-song score was 26; the 1st percentile of clean 10 s positives was 156." (Source: `eval/results/queries_val_seed0.csv.gz`.)

### Q2. Top-30 peaks per 1 s block, blocks counted from the start of the audio. Why can a query and its song keep different peaks?
**Sean:** Because the blocks start at different points so different peaks win.

**Feedback:** Correct. Add a concrete example and the fix with its cost.

**Strong answer:** "Block boundaries are anchored to the audio start, so a query and its source rank peaks against different neighbors and keep different subsets; every peak kept in one but not the other destroys its landmarks. Fix: rank each peak within a window centered on itself, which is shift-invariant, at the cost of exact density control."

### Q3. What's the weakness of finding duplicates with our own matcher, then grading that matcher with those labels?
**Sean:** It's circular: if the matcher misses a duplicate we never find out.

**Feedback:** Correct. Add the direction of the bias and the mitigation.

**Strong answer:** "It's circular: duplicates the matcher can't see stay unlabeled. But that error shows up as false accepts, so it makes FAR pessimistic, not flattering. We set the clustering threshold low because over-grouping is cheap, spot-checked pairs by metadata, and an independent metadata dedupe would close the loop. In production, dedupe at catalog ingest using rights metadata plus audio."

**Pattern to work on:** answers name the right idea in one line. Add mechanism, a number, and the tradeoff.

## Pending (Sean deferred the quiz; answer before this goes on a resume)

### Go fingerprinter port + profiling (D16-D18)
1. Your first Go version was 5x slower than numpy. Walk through how you found out why, and what the fix was. Why didn't you just switch FFT libraries?
2. Why did switching Python to float64 matter for testing, and what would "Jaccard >= 0.98" have hidden?
3. Why does the Fingerprinter carry scratch buffers, and what goes wrong if two goroutines share one?

### Segment format (D19-D21)
1. What does mmap give you over reading the file into memory? What happens on the first query after a reboot (cold page cache)?
2. Why is the write temp-file, fsync, rename? What can a crash leave behind with a plain write?
3. A fuzzer fed the reader 47M mangled files. What property is being tested, and why does it matter for a server?

### Matcher + shard server (D22-D23)
1. Why sort-and-count instead of a hash map? What would you try if the matcher became the bottleneck?
2. The shard has one matcher per core and rejects requests that wait over 100 ms. Why is rejecting work good for latency?
3. Shutdown waits for in-flight searches before unmapping the segment. What happens if it doesn't?

### Coordinator + replication (D24-D26)
1. A replica hangs instead of crashing. Walk through what a query does, with times, given a 200 ms deadline.
2. Why does an overloaded replica not get ejected, when a failing one does?
3. Replication here has no consensus protocol. Why is that safe, and what change to the system would make you need one?
