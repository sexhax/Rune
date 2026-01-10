// API Base URL
const API_BASE = '/api';

// State
let currentConfig = null;
let statsInterval = null;

// Initialize on page load
document.addEventListener('DOMContentLoaded', () => {
    loadConfig();
    loadStats();
    setupEventListeners();
    
    // Auto-refresh stats and config every 5 seconds
    statsInterval = setInterval(() => {
        loadStats();
        loadConfig();
    }, 5000);
});

// Setup event listeners
function setupEventListeners() {
    // Toggle switches
    document.getElementById('autoResponderToggle').addEventListener('change', handleToggleAutoResponder);
    document.getElementById('autoEmojiToggle').addEventListener('change', handleToggleAutoEmoji);
    
    // Status buttons
    document.getElementById('statusOnline').addEventListener('click', () => updateStatus('online'));
    document.getElementById('statusIdle').addEventListener('click', () => updateStatus('idle'));
    document.getElementById('statusDnd').addEventListener('click', () => updateStatus('dnd'));
    document.getElementById('statusInvisible').addEventListener('click', () => updateStatus('invisible'));
    
    // Stop auto pressure button
    document.getElementById('stopAutoPressureBtn').addEventListener('click', stopAutoPressure);
    
    // Save button
    document.getElementById('saveBtn').addEventListener('click', saveSettings);
}

// Load configuration from API
async function loadConfig() {
    try {
        const response = await fetch(`${API_BASE}/config`);
        if (!response.ok) throw new Error('Failed to load config');
        
        currentConfig = await response.json();
        updateUIFromConfig(currentConfig);
    } catch (error) {
        showToast('Failed to load configuration', 'error');
        console.error('Error loading config:', error);
    }
}

// Update UI from config
function updateUIFromConfig(config) {
    // Update toggles
    document.getElementById('autoResponderToggle').checked = config.auto_response_enabled;
    document.getElementById('autoEmojiToggle').checked = config.auto_emoji_enabled;
    
    // Update inputs only if they don't have focus (user isn't typing)
    const prefixInput = document.getElementById('prefixInput');
    if (document.activeElement !== prefixInput) {
        prefixInput.value = config.prefix || '';
    }
    
    const phraseInput = document.getElementById('autoResponsePhraseInput');
    if (document.activeElement !== phraseInput) {
        phraseInput.value = config.auto_response_phrase || '';
    }
    
    const emojiInput = document.getElementById('autoEmojiInput');
    if (document.activeElement !== emojiInput) {
        emojiInput.value = config.auto_emoji || '';
    }
    
    // Update custom status text input only if it doesn't have focus
    const customStatusInput = document.getElementById('customStatusTextInput');
    if (document.activeElement !== customStatusInput) {
        // Note: custom status text isn't stored in config, so we don't update it from config
        // This prevents overwriting user input
    }
    
    // Update status button states
    updateStatusButtonStates(config.current_status);
    
    // Update auto pressure button
    const stopBtn = document.getElementById('stopAutoPressureBtn');
    if (config.auto_pressure_active) {
        stopBtn.disabled = false;
        stopBtn.textContent = 'Stop Auto Pressure';
    } else {
        stopBtn.disabled = true;
        stopBtn.textContent = 'Auto Pressure Not Active';
    }
}

// Update status button states
function updateStatusButtonStates(currentStatus) {
    const buttons = {
        'online': document.getElementById('statusOnline'),
        'idle': document.getElementById('statusIdle'),
        'dnd': document.getElementById('statusDnd'),
        'invisible': document.getElementById('statusInvisible')
    };
    
    // Reset all buttons
    Object.values(buttons).forEach(btn => {
        btn.classList.remove('ring-4', 'ring-cyan-500');
    });
    
    // Highlight current status
    if (buttons[currentStatus]) {
        buttons[currentStatus].classList.add('ring-4', 'ring-cyan-500');
    }
}

// Load statistics
async function loadStats() {
    try {
        const response = await fetch(`${API_BASE}/stats`);
        if (!response.ok) throw new Error('Failed to load stats');
        
        const stats = await response.json();
        updateStatsUI(stats);
    } catch (error) {
        console.error('Error loading stats:', error);
    }
}

// Update statistics UI
function updateStatsUI(stats) {
    const uptimeText = `${stats.uptime_days}d ${stats.uptime_hours}h ${stats.uptime_minutes}m`;
    document.getElementById('uptime').textContent = uptimeText;
    document.getElementById('commandsHandled').textContent = stats.commands_handled.toLocaleString();
    document.getElementById('messagesLogged').textContent = stats.messages_logged.toLocaleString();
}

// Handle auto responder toggle
async function handleToggleAutoResponder(event) {
    const enabled = event.target.checked;
    
    try {
        const response = await fetch(`${API_BASE}/toggle/autoresponder`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' }
        });
        
        if (!response.ok) throw new Error('Failed to toggle auto responder');
        
        const result = await response.json();
        showToast(result.message, 'success');
        
        // Reload config to sync state
        loadConfig();
    } catch (error) {
        // Revert toggle on error
        event.target.checked = !enabled;
        showToast('Failed to toggle auto responder', 'error');
        console.error('Error toggling auto responder:', error);
    }
}

// Handle auto emoji toggle
async function handleToggleAutoEmoji(event) {
    const enabled = event.target.checked;
    
    try {
        const response = await fetch(`${API_BASE}/toggle/autoemoji`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' }
        });
        
        if (!response.ok) throw new Error('Failed to toggle auto emoji');
        
        const result = await response.json();
        showToast(result.message, 'success');
        
        // Reload config to sync state
        loadConfig();
    } catch (error) {
        // Revert toggle on error
        event.target.checked = !enabled;
        showToast('Failed to toggle auto emoji', 'error');
        console.error('Error toggling auto emoji:', error);
    }
}

// Update Discord status
async function updateStatus(status) {
    try {
        // Get custom status text if provided
        const customTextInput = document.getElementById('customStatusTextInput');
        const customText = customTextInput.value.trim();
        
        const payload = { status };
        if (customText) {
            payload.custom_text = customText;
        }
        
        const response = await fetch(`${API_BASE}/status`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(payload)
        });
        
        if (!response.ok) {
            const error = await response.json();
            throw new Error(error.message || 'Failed to update status');
        }
        
        const result = await response.json();
        showToast(result.message, 'success');
        
        // Update UI
        updateStatusButtonStates(status);
        
        // Reload config to sync state
        loadConfig();
    } catch (error) {
        showToast(error.message || 'Failed to update status', 'error');
        console.error('Error updating status:', error);
    }
}

// Stop auto pressure
async function stopAutoPressure() {
    const btn = document.getElementById('stopAutoPressureBtn');
    btn.disabled = true;
    btn.textContent = 'Stopping...';
    
    try {
        const response = await fetch(`${API_BASE}/autopressure/stop`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' }
        });
        
        if (!response.ok) throw new Error('Failed to stop auto pressure');
        
        const result = await response.json();
        showToast(result.message, result.stopped ? 'success' : 'info');
        
        if (result.stopped) {
            btn.disabled = true;
            btn.textContent = 'Auto Pressure Not Active';
            // Reload config to sync state
            loadConfig();
        }
    } catch (error) {
        btn.disabled = false;
        btn.textContent = 'Stop Auto Pressure';
        showToast('Failed to stop auto pressure', 'error');
        console.error('Error stopping auto pressure:', error);
    }
}

// Save settings
async function saveSettings() {
    const btn = document.getElementById('saveBtn');
    const originalText = btn.textContent;
    btn.disabled = true;
    btn.textContent = 'Saving...';
    
    try {
        const updates = {
            prefix: document.getElementById('prefixInput').value.trim(),
            auto_response_phrase: document.getElementById('autoResponsePhraseInput').value.trim(),
            auto_emoji: document.getElementById('autoEmojiInput').value.trim()
        };
        
        // Only send non-empty values
        const payload = {};
        if (updates.prefix) payload.prefix = updates.prefix;
        if (updates.auto_response_phrase) payload.auto_response_phrase = updates.auto_response_phrase;
        if (updates.auto_emoji) payload.auto_emoji = updates.auto_emoji;
        
        const response = await fetch(`${API_BASE}/config`, {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(payload)
        });
        
        if (!response.ok) {
            const error = await response.json();
            throw new Error(error.message || 'Failed to save settings');
        }
        
        const updatedConfig = await response.json();
        currentConfig = updatedConfig;
        updateUIFromConfig(updatedConfig);
        
        showToast('Settings saved successfully', 'success');
    } catch (error) {
        showToast(error.message || 'Failed to save settings', 'error');
        console.error('Error saving settings:', error);
    } finally {
        btn.disabled = false;
        btn.textContent = originalText;
    }
}

// Show toast notification
function showToast(message, type = 'info') {
    const container = document.getElementById('toastContainer');
    const toast = document.createElement('div');
    
    const colors = {
        success: 'bg-green-600',
        error: 'bg-red-600',
        info: 'bg-blue-600',
        warning: 'bg-yellow-600'
    };
    
    toast.className = `toast ${colors[type] || colors.info} text-white px-6 py-3 rounded-lg shadow-lg flex items-center gap-2`;
    toast.innerHTML = `
        <span>${message}</span>
        <button class="ml-4 text-white hover:text-gray-200" onclick="this.parentElement.remove()">×</button>
    `;
    
    container.appendChild(toast);
    
    // Auto remove after 5 seconds
    setTimeout(() => {
        toast.style.opacity = '0';
        setTimeout(() => toast.remove(), 300);
    }, 5000);
}

// Check auto pressure status periodically (already handled by loadConfig in stats refresh)
