INSERT INTO configs(key, value, updated_at)
SELECT
    'emby_proxy_instances',
    json_array(json_object(
        'id', 'default',
        'name', 'Emby',
        'emby_url', COALESCE((SELECT value FROM configs WHERE key = 'emby_url'), ''),
        'api_key', COALESCE((SELECT value FROM configs WHERE key = 'emby_api_key'), ''),
        'proxy_port', COALESCE((SELECT value FROM configs WHERE key = 'emby_proxy_port'), '')
    )),
    CURRENT_TIMESTAMP
WHERE NOT EXISTS (SELECT 1 FROM configs WHERE key = 'emby_proxy_instances')
  AND EXISTS (
      SELECT 1 FROM configs
      WHERE key IN ('emby_url', 'emby_api_key', 'emby_proxy_port')
        AND trim(value) <> ''
  );

DELETE FROM configs
WHERE key IN ('emby_url', 'emby_api_key', 'emby_proxy_port')
  AND EXISTS (SELECT 1 FROM configs WHERE key = 'emby_proxy_instances');
