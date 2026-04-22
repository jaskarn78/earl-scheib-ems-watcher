# Deferred items (out of scope for 260422-wmh)

Pre-existing test failures and issues discovered during this task that are NOT caused by template-editor changes.

## tests/test_queue_endpoint.py::test_get_queue_happy_path
- **Status**: failing before this quick task started
- **Reason**: Test asserts queue row keys == {id, doc_id, job_type, phone, name, send_at, created_at}, but the GET /queue endpoint (updated in quick task QAJ-01) now also returns vin, vehicle_desc, ro_id, email, address, sent_at, estimate_key.
- **Not fixed here**: Out of scope per SCOPE BOUNDARY — this test is pre-existing, not caused by template editor changes. Fix is a trivial one-line update to expected_keys, but it belongs to a separate debug/quick task.
