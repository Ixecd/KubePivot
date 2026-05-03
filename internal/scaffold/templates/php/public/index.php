<?php
$path = parse_url($_SERVER['REQUEST_URI'], PHP_URL_PATH);
header('Content-Type: application/json');
if ($path === '/healthz') { echo json_encode(['status' => 'ok']); }
else { echo json_encode(['service' => '{{name}}', 'status' => 'ok']); }
