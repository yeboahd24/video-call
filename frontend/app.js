/* ─────────────────────────────────────────────
   GoVideo — frontend/app.js
   Signaling via PostgreSQL WAL ↔ Go WebSocket
───────────────────────────────────────────── */

const ICE_SERVERS = [
  { urls: 'stun:stun.l.google.com:19302' },
  { urls: 'stun:stun1.l.google.com:19302' },
  // Add TURN here for production:
  // { urls: 'turn:your.turn.server', username: '...', credential: '...' }
];

// ── State ──────────────────────────────────────
let myID     = '';
let roomID   = '';
let ws       = null;
let localStream     = null;
let screenStream    = null;
let micMuted        = false;
let camOff          = false;

/** @type {Map<string, RTCPeerConnection>} */
const peers  = new Map();

// ── DOM refs ───────────────────────────────────
const $ = id => document.getElementById(id);
const lobbyEl   = $('lobby');
const callEl    = $('call');
const videosEl  = $('videos');
const statusEl  = $('status');

// ── Lobby handlers ─────────────────────────────
$('btn-create').onclick = async () => {
  const name = $('username').value.trim();
  if (!name) return showLobbyError('Enter your name first');
  try {
    const res  = await fetch('/rooms', { method: 'POST' });
    const data = await res.json();
    enterCall(data.room_id, name);
  } catch (e) {
    showLobbyError('Could not create room: ' + e.message);
  }
};

$('btn-join').onclick = () => {
  const name = $('username').value.trim();
  const room = $('room-input').value.trim();
  if (!name) return showLobbyError('Enter your name first');
  if (!room) return showLobbyError('Paste a room ID');
  enterCall(room, name);
};

function showLobbyError(msg) {
  $('lobby-error').textContent = msg;
}

// ── Enter call ─────────────────────────────────
async function enterCall(room, name) {
  roomID = room;
  myID   = name + '_' + Math.random().toString(36).slice(2, 6);

  // Get camera + mic
  try {
    localStream = await navigator.mediaDevices.getUserMedia({ video: true, audio: true });
  } catch (e) {
    showLobbyError('Camera/mic access denied: ' + e.message);
    return;
  }

  // Show call UI
  lobbyEl.classList.add('hidden');
  callEl.classList.remove('hidden');
  $('room-label').textContent = 'Room: ' + roomID;

  // Render local video
  addVideoTile('me', myID + ' (you)', localStream, true);

  // Connect WebSocket
  connectWS();

  // Fetch existing participants and offer to each
  try {
    const res   = await fetch(`/rooms/${roomID}`);
    const data  = await res.json();
    for (const uid of (data.participants || [])) {
      if (uid !== myID) {
        await callPeer(uid);
      }
    }
  } catch (_) { /* room might be empty */ }

  setStatus('Connected · ' + roomID);
}

// ── WebSocket ──────────────────────────────────
function connectWS() {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  ws = new WebSocket(`${proto}://${location.host}/ws?room=${roomID}&user=${myID}`);

  ws.onopen  = () => setStatus('Signaling connected');
  ws.onclose = () => setStatus('Signaling disconnected — refresh to reconnect');
  ws.onerror = e  => console.error('ws error', e);

  ws.onmessage = async ({ data }) => {
    let msg;
    try { msg = JSON.parse(data); } catch { return; }

    const from = msg.from_user;

    switch (msg.type) {
      case 'join':
        // A new peer joined — send them an offer
        if (from !== myID) await callPeer(from);
        break;

      case 'offer': {
        const pc = getOrCreatePeer(from);
        await pc.setRemoteDescription({ type: 'offer', sdp: msg.sdp });
        const answer = await pc.createAnswer();
        await pc.setLocalDescription(answer);
        send({ type: 'answer', to_user: from, sdp: answer.sdp });
        break;
      }

      case 'answer': {
        const pc = peers.get(from);
        if (pc) await pc.setRemoteDescription({ type: 'answer', sdp: msg.sdp });
        break;
      }

      case 'ice': {
        const pc = peers.get(from);
        if (pc && msg.candidate) {
          try { await pc.addIceCandidate(msg.candidate); } catch (_) {}
        }
        break;
      }

      case 'leave':
        closePeer(from);
        break;
    }
  };
}

function send(obj) {
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify(obj));
  }
}

// ── Peer connections ───────────────────────────
async function callPeer(remoteUser) {
  const pc = getOrCreatePeer(remoteUser);
  const offer = await pc.createOffer();
  await pc.setLocalDescription(offer);
  send({ type: 'offer', to_user: remoteUser, sdp: offer.sdp });
}

function getOrCreatePeer(remoteUser) {
  if (peers.has(remoteUser)) return peers.get(remoteUser);

  const pc = new RTCPeerConnection({ iceServers: ICE_SERVERS });
  peers.set(remoteUser, pc);

  // Add local tracks
  localStream.getTracks().forEach(t => pc.addTrack(t, localStream));

  // ICE candidates → WAL via WebSocket
  pc.onicecandidate = ({ candidate }) => {
    if (candidate) send({ type: 'ice', to_user: remoteUser, candidate });
  };

  // Remote tracks → video tile
  pc.ontrack = ({ streams }) => {
    if (streams[0]) addVideoTile(remoteUser, remoteUser, streams[0], false);
  };

  // Connection state UI
  pc.onconnectionstatechange = () => {
    updateConnState(remoteUser, pc.connectionState);
    if (pc.connectionState === 'failed' || pc.connectionState === 'disconnected') {
      closePeer(remoteUser);
    }
  };

  return pc;
}

function closePeer(remoteUser) {
  const pc = peers.get(remoteUser);
  if (pc) { pc.close(); peers.delete(remoteUser); }
  removeVideoTile(remoteUser);
}

// ── Video tiles ────────────────────────────────
function addVideoTile(id, label, stream, muted) {
  if (document.getElementById('tile-' + id)) return;

  const wrap  = document.createElement('div');
  wrap.className = 'video-wrap';
  wrap.id = 'tile-' + id;

  const vid = document.createElement('video');
  vid.autoplay = true;
  vid.playsInline = true;
  vid.muted = muted;
  vid.srcObject = stream;

  const lbl = document.createElement('span');
  lbl.className = 'label';
  lbl.textContent = label;

  const state = document.createElement('span');
  state.className = 'conn-state';
  state.id = 'state-' + id;

  wrap.append(vid, lbl, state);
  videosEl.appendChild(wrap);
  updateGrid();
}

function removeVideoTile(id) {
  const el = document.getElementById('tile-' + id);
  if (el) el.remove();
  updateGrid();
}

function updateGrid() {
  const count = videosEl.children.length;
  videosEl.className = 'peers-' + Math.max(1, count);
}

function updateConnState(id, state) {
  const el = document.getElementById('state-' + id);
  if (el) el.textContent = state;
}

// ── Controls ───────────────────────────────────
$('btn-copy').onclick = () => {
  navigator.clipboard.writeText(roomID);
  $('btn-copy').textContent = '✅ Copied!';
  setTimeout(() => ($('btn-copy').textContent = '📋 Copy ID'), 2000);
};

$('btn-mute').onclick = () => {
  micMuted = !micMuted;
  localStream.getAudioTracks().forEach(t => (t.enabled = !micMuted));
  $('btn-mute').textContent = micMuted ? '🔇 Unmute' : '🎤 Mute';
  $('btn-mute').classList.toggle('muted', micMuted);
};

$('btn-cam').onclick = () => {
  camOff = !camOff;
  localStream.getVideoTracks().forEach(t => (t.enabled = !camOff));
  $('btn-cam').textContent = camOff ? '📷 Start Cam' : '📷 Stop Cam';
  $('btn-cam').classList.toggle('muted', camOff);
};

$('btn-screen').onclick = async () => {
  if (screenStream) {
    // Stop screen share — swap back to camera
    screenStream.getTracks().forEach(t => t.stop());
    screenStream = null;
    $('btn-screen').textContent = '🖥 Share Screen';
    $('btn-screen').classList.remove('muted');

    const camTrack = localStream.getVideoTracks()[0];
    replaceVideoTrack(camTrack);
    return;
  }

  try {
    screenStream = await navigator.mediaDevices.getDisplayMedia({ video: true });
    const screenTrack = screenStream.getVideoTracks()[0];
    $('btn-screen').textContent = '🖥 Stop Share';
    $('btn-screen').classList.add('muted');

    // Replace video track on all peer connections
    replaceVideoTrack(screenTrack);

    // Auto-revert when user stops via browser UI
    screenTrack.onended = () => $('btn-screen').click();
  } catch (e) {
    setStatus('Screen share cancelled');
  }
};

function replaceVideoTrack(newTrack) {
  // Update local preview
  const localVid = document.querySelector('#tile-me video');
  if (localVid) {
    const s = new MediaStream([newTrack, ...localStream.getAudioTracks()]);
    localVid.srcObject = s;
  }
  // Replace on each peer connection
  peers.forEach(pc => {
    const sender = pc.getSenders().find(s => s.track?.kind === 'video');
    if (sender) sender.replaceTrack(newTrack);
  });
}

$('btn-leave').onclick = () => {
  peers.forEach((_, uid) => closePeer(uid));
  ws?.close();
  localStream?.getTracks().forEach(t => t.stop());
  screenStream?.getTracks().forEach(t => t.stop());
  location.reload();
};

// ── Helpers ────────────────────────────────────
function setStatus(msg) {
  statusEl.textContent = msg;
}
