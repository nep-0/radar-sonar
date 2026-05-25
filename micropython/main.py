from machine import Pin, UART
import time


SONAR_UART_ID = 1
SONAR_RX_PIN = 5
SONAR_BAUD = 9600

RADAR_UART_ID = 0
RADAR_TX_PIN = 0
RADAR_RX_PIN = 1
RADAR_BAUD = 256000

SONAR_OVERRANGE_MM = 0xAAAA
SONAR_READ_TIMEOUT_MS = 60
RADAR_READ_TIMEOUT_MS = 140

RADAR_FRAME_LEN = 30
RADAR_HEADER = b"\xaa\xff\x03\x00"
RADAR_TRAILER = b"\x55\xcc"
RADAR_TARGETS = 3
RADAR_TARGET_LEN = 8

DEFAULT_HEIGHT_MM = 0.0
DEFAULT_OBSTACLE = "left"
DEFAULT_SONAR_STATUS = "unknown"


sonar = UART(
    SONAR_UART_ID,
    baudrate=SONAR_BAUD,
    bits=8,
    parity=None,
    stop=1,
    rx=Pin(SONAR_RX_PIN),
)

radar = UART(
    RADAR_UART_ID,
    baudrate=RADAR_BAUD,
    bits=8,
    parity=None,
    stop=1,
    tx=Pin(RADAR_TX_PIN),
    rx=Pin(RADAR_RX_PIN),
)

_sonar_buf = bytearray()
_radar_buf = bytearray()
_last_height_mm = DEFAULT_HEIGHT_MM
_last_obstacle = DEFAULT_OBSTACLE
_last_radar_targets = []
_last_sonar_status = DEFAULT_SONAR_STATUS


def _millis():
    return time.ticks_ms()


def _expired(start_ms, timeout_ms):
    return time.ticks_diff(_millis(), start_ms) >= timeout_ms


def _read_available(uart):
    waiting = uart.any()
    if waiting:
        return uart.read(waiting)
    return None


def _decode_ld2450_value(lo, hi):
    raw = lo | (hi << 8)
    value = raw & 0x7FFF
    if raw & 0x8000:
        return value
    return -value


def _parse_sonar_frames():
    global _last_height_mm, _sonar_buf, _last_sonar_status

    buf = _sonar_buf
    i = 0
    limit = len(buf)

    while i + 4 <= limit:
        if buf[i] != 0xFF:
            i += 1
            continue

        frame = buf[i : i + 4]
        checksum = (frame[0] + frame[1] + frame[2]) & 0xFF
        if checksum != frame[3]:
            i += 1
            continue

        distance_mm = (frame[1] << 8) | frame[2]
        if distance_mm == SONAR_OVERRANGE_MM:
            _last_sonar_status = "overrange"
        else:
            _last_height_mm = float(distance_mm)
            _last_sonar_status = "normal"
        i += 4

    if i:
        _sonar_buf = bytearray(buf[i:])


def _parse_radar_frames():
    global _last_obstacle, _last_radar_targets, _radar_buf

    buf = _radar_buf
    i = 0

    while i + RADAR_FRAME_LEN <= len(buf):
        header_at = buf.find(RADAR_HEADER, i)
        if header_at < 0:
            keep = min(len(buf), len(RADAR_HEADER) - 1)
            _radar_buf = bytearray(buf[len(buf) - keep :])
            return

        if header_at + RADAR_FRAME_LEN > len(buf):
            break

        frame = buf[header_at : header_at + RADAR_FRAME_LEN]
        if frame[-2:] != RADAR_TRAILER:
            i = header_at + 1
            continue

        targets = []
        offset = len(RADAR_HEADER)
        for _ in range(RADAR_TARGETS):
            chunk = frame[offset : offset + RADAR_TARGET_LEN]
            offset += RADAR_TARGET_LEN
            if not any(chunk):
                continue

            x_mm = _decode_ld2450_value(chunk[0], chunk[1])
            y_mm = _decode_ld2450_value(chunk[2], chunk[3])
            speed_cms = _decode_ld2450_value(chunk[4], chunk[5])
            resolution_mm = chunk[6] | (chunk[7] << 8)
            targets.append(
                {
                    "x_mm": x_mm,
                    "y_mm": y_mm,
                    "speed_cms": speed_cms,
                    "resolution_mm": resolution_mm,
                }
            )

        if targets:
            _last_radar_targets = targets
            nearest = min(targets, key=lambda target: abs(target["y_mm"]))
            _last_obstacle = "right" if nearest["x_mm"] >= 0 else "left"

        i = header_at + RADAR_FRAME_LEN

    if i:
        _radar_buf = bytearray(buf[i:])


def _update_sonar(timeout_ms=SONAR_READ_TIMEOUT_MS):
    start = _millis()
    while True:
        data = _read_available(sonar)
        if data:
            _sonar_buf.extend(data)
            _parse_sonar_frames()
        if data is None or _expired(start, timeout_ms):
            return


def _update_radar(timeout_ms=RADAR_READ_TIMEOUT_MS):
    start = _millis()
    while True:
        data = _read_available(radar)
        if data:
            _radar_buf.extend(data)
            _parse_radar_frames()
        if data is None or _expired(start, timeout_ms):
            return


def update():
    _update_sonar()
    _update_radar()


def get_height():
    _update_sonar()
    return _last_height_mm


def get_sonar_status():
    _update_sonar()
    return _last_sonar_status


def get_obstacle():
    _update_radar()
    return _last_obstacle


def get_radar_targets():
    _update_radar()
    return _last_radar_targets
