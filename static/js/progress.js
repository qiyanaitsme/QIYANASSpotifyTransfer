let socket;

document.getElementById('restoreForm').onsubmit = function(e) {
    e.preventDefault();

    const formData = new FormData(this);
    const progressContainer = document.getElementById('progressContainer');
    const progressBar = document.getElementById('progressBar');
    const progressText = document.getElementById('progressText');
    const currentPlaylist = document.getElementById('currentPlaylist');
    const currentTrack = document.getElementById('currentTrack');

    progressContainer.style.display = 'block';

    socket = new WebSocket(`ws://${window.location.host}/ws`);

    socket.onmessage = function(event) {
        const data = JSON.parse(event.data);
        progressBar.style.width = data.progress + '%';
        progressText.textContent = `${Math.round(data.progress)}%`;
        currentPlaylist.textContent = `Processing: ${data.playlist}`;
        currentTrack.textContent = `Current track: ${data.currentTrack}`;
    };

    fetch('/restore', {
        method: 'POST',
        body: formData
    }).then(response => {
        if (response.ok) {
            window.location.href = '/dashboard';
        }
    });
};
