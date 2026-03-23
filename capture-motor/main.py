import asyncio
import json
import logging
import os
import random
import string
import sys
import threading

import socket
import socketio
from aiortc import RTCPeerConnection, RTCSessionDescription, VideoStreamTrack, RTCIceCandidate, RTCConfiguration, RTCIceServer
from av import VideoFrame
import mss
import pyautogui
import numpy as np

import cv2

# Performance tuning
pyautogui.FAILSAFE = False
pyautogui.PAUSE = 0
logging.basicConfig(level=logging.ERROR)

SIGNALING_SERVER_URL = os.getenv("VELODESK_SIGNALING_URL", "https://velodesk-signaling.onrender.com")
CONFIG_FILE = "velodesk_config.json"

# WebRTC Config (Match Client)
rtc_config = RTCConfiguration(iceServers=[RTCIceServer(urls="stun:stun.l.google.com:19302")])

# Estado Global
app_config = {}
pc = None
sio = socketio.AsyncClient(reconnection=True, logger=False, engineio_logger=False)

def get_hostname():
    try: return socket.gethostname()
    except: return "VeloDesk Device"

def load_config():
    if os.path.exists(CONFIG_FILE):
        with open(CONFIG_FILE, "r") as f: 
            cfg = json.load(f)
            cfg["hostname"] = get_hostname()
            return cfg
    else:
        new_id = "".join([str(random.randint(0,9)) for _ in range(9)])
        new_pass = "".join(random.choices(string.ascii_lowercase + string.digits, k=8))
        cfg = {"remoteId": new_id, "password": new_pass, "hostname": get_hostname()}
        save_config(cfg)
        return cfg

def save_config(cfg):
    with open(CONFIG_FILE, "w") as f: json.dump(cfg, f)

app_config = load_config()

class ScreenVideoTrack(VideoStreamTrack):
    def __init__(self):
        super().__init__()
        self.sct = mss.mss()
        self.monitor = self.sct.monitors[1]
        print(f"[CAPTURE] Inciando captura no Monitor 1: {self.monitor}", flush=True)
        
    async def recv(self):
        pts, time_base = await self.next_timestamp()
        
        # Capture Screen
        sct_img = self.sct.grab(self.monitor)
        # Garantir que seja um array editável e no formato correto
        img = np.array(sct_img, copy=True)[:, :, :3]
        
        # Draw Mouse Cursor
        try:
            mx, my = pyautogui.position()
            local_x = mx - self.monitor["left"]
            local_y = my - self.monitor["top"]
            
            # Verificar se está dentro do monitor capturado
            if 0 <= local_x < self.monitor["width"] and 0 <= local_y < self.monitor["height"]:
                # Desenhar cursor maior e mais chamativo para teste (Magenta)
                cv2.circle(img, (local_x, local_y), 10, (255, 0, 255), -1)
                cv2.circle(img, (local_x, local_y), 12, (255, 255, 255), 2)
        except Exception as e:
            if random.random() < 0.01: print(f"Erro cursor: {e}")
            
        frame = VideoFrame.from_ndarray(img, format="bgr24")
        frame.pts = pts
        frame.time_base = time_base
        return frame

def create_pc(loop, target_id):
    global pc
    pc = RTCPeerConnection(configuration=rtc_config)
    
    # Criar track fresca para cada conexão
    track = ScreenVideoTrack()
    pc.addTrack(track)

    @pc.on("datachannel")
    def on_datachannel(channel):
        print(f"[RTC] Canal de Entrada Aberto: {channel.label}", flush=True)
        if channel.label == "input":
            @channel.on("message")
            def on_input(message):
                try:
                    cmd = json.loads(message)
                    act = cmd.get("action")
                    if act == "heartbeat":
                        if random.random() < 0.1: print("[INPUT] Heartbeat OK", flush=True)
                        return
                        
                    if act == "mousemove":
                        scr_w, scr_h = pyautogui.size()
                        tx, ty = int(cmd["x"] * scr_w), int(cmd["y"] * scr_h)
                        pyautogui.moveTo(tx, ty)
                        if random.random() < 0.05:
                            print(f"[INPUT] Mouse: {tx}, {ty} (Size: {scr_w}x{scr_h})", flush=True)
                    elif act == "mousedown": 
                        print(f"[INPUT] Click: {cmd.get('button')}", flush=True)
                        pyautogui.mouseDown(button=cmd.get("button", "left"))
                    elif act == "mouseup": pyautogui.mouseUp(button=cmd.get("button", "left"))
                    elif act == "keydown": 
                        print(f"[INPUT] Key: {cmd.get('key')}", flush=True)
                        pyautogui.keyDown(cmd.get("key"))
                    elif act == "keyup": pyautogui.keyUp(cmd.get("key"))
                    elif act == "scroll": pyautogui.scroll(cmd.get("delta", 0))
                except Exception as e:
                    print(f"[INPUT] Erro: {e}", flush=True)

    @pc.on("connectionstatechange")
    def on_state():
        print(f"[RTC] State: {pc.connectionState}", flush=True)

    @pc.on("iceconnectionstatechange")
    def on_ice():
        print(f"[RTC] ICE: {pc.iceConnectionState}", flush=True)

    # TRICKLE ICE: Mandar candidatos conforme saem
    @pc.on("icegatheringstatechange")
    def on_gathering():
        if pc.iceGatheringState == "complete":
            print("[RTC] ICE Gathering Complete", flush=True)

    return pc

@sio.event
async def connect():
    print(f"[SIGNaling] ONLINE | ID: {app_config['remoteId']}", flush=True)
    await sio.emit('register-host', app_config)

@sio.on('host-registered')
async def on_reg(data):
    print(f"##VELODESK:{json.dumps(data)}##", flush=True)

@sio.on('signal')
async def on_signal(data):
    loop = asyncio.get_event_loop()
    from_id = data.get('from')
    payload = data.get('signal')
    if not from_id or not payload: return
    
    global pc
    try:
        if payload.get('action') == 'end-session':
            print("[RTC] Sessão encerrada pelo Viewer.", flush=True)
            if pc:
                await pc.close()
            pc = None
            return

        if 'type' in payload and payload['type'] == 'offer':
            print(f"[RTC] Recebida oferta de {from_id}. Reiniciando conexão...", flush=True)
            if pc:
                await pc.close()
            pc = create_pc(loop, from_id)
            
            await pc.setRemoteDescription(RTCSessionDescription(sdp=payload['sdp'], type=payload['type']))
            answer = await pc.createAnswer()
            await pc.setLocalDescription(answer)
            
            # Esperar um pouco para colher alguns candidatos locais antes de enviar o SDP
            # Isso ajuda muito em redes com NAT/Firewall
            await asyncio.sleep(0.5) 
            
            await sio.emit('signal', {'to': from_id, 'signal': {'type': pc.localDescription.type, 'sdp': pc.localDescription.sdp}})
        elif 'type' in payload and payload['type'] == 'answer':
            await pc.setRemoteDescription(RTCSessionDescription(sdp=payload['sdp'], type=payload['type']))
        elif 'candidate' in payload:
            cand = RTCIceCandidate(
                candidate=payload.get('candidate'),
                sdpMid=payload.get('sdpMid'),
                sdpMLineIndex=payload.get('sdpMLineIndex')
            )
            await pc.addIceCandidate(cand)
    except Exception as e:
        print(f"[RTC] Erro Handshake: {e}", flush=True)

def listen_stdin(loop):
    for line in sys.stdin:
        try:
            if not line.strip(): continue
            cmd = json.loads(line.strip())
            if cmd.get("action") == "update-password":
                app_config["password"] = cmd.get("password")
                save_config(app_config)
                asyncio.run_coroutine_threadsafe(sio.emit('register-host', app_config), loop)
        except Exception: pass

async def main():
    loop = asyncio.get_running_loop()
    threading.Thread(target=listen_stdin, args=(loop,), daemon=True).start()
    
    while True:
        try:
            if not sio.connected:
                await sio.connect(SIGNALING_SERVER_URL, wait_timeout=20)
            await sio.wait()
        except asyncio.CancelledError: break
        except Exception as e:
            await asyncio.sleep(5)

if __name__ == "__main__":
    try:
        asyncio.run(main())
    except KeyboardInterrupt:
        print("[ENGINE] Desligando...", flush=True)
