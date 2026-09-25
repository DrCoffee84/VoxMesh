package com.voxmesh.mic

import android.Manifest
import android.content.Context
import android.content.Intent
import android.content.SharedPreferences
import android.content.pm.PackageManager
import android.media.AudioDeviceInfo
import android.media.AudioManager
import android.media.MediaRecorder
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.widget.ArrayAdapter
import android.widget.SeekBar
import android.widget.Toast
import androidx.activity.result.contract.ActivityResultContracts
import androidx.appcompat.app.AppCompatActivity
import androidx.core.content.ContextCompat
import com.google.mlkit.vision.barcode.common.Barcode
import com.google.mlkit.vision.codescanner.GmsBarcodeScannerOptions
import com.google.mlkit.vision.codescanner.GmsBarcodeScanning
import com.voxmesh.mic.databinding.ActivityMainBinding

data class AudioSourceOption(val source: Int, val label: String) {
    override fun toString(): String = label
}

data class AudioDeviceOption(val id: Int, val label: String) {
    override fun toString(): String = label
}

class MainActivity : AppCompatActivity() {

    private lateinit var binding: ActivityMainBinding
    private lateinit var prefs: SharedPreferences

    companion object {
        private const val PREFS_NAME = "voxmesh_mic_prefs"
        private const val KEY_HOST = "saved_host"
        private const val KEY_PORT = "saved_port"
        private const val KEY_TOKEN = "saved_token"
        private const val KEY_GAIN_STEP = "saved_gain_step"
        private const val KEY_AUDIO_SOURCE = "saved_audio_source"
        private const val KEY_DEVICE_ID = "saved_device_id"
    }

    private val permissionLauncher = registerForActivityResult(
        ActivityResultContracts.RequestMultiplePermissions()
    ) { permissions ->
        val audioGranted = permissions[Manifest.permission.RECORD_AUDIO] == true
        if (audioGranted) {
            startMicService()
        } else {
            Toast.makeText(this, "Se requiere permiso de micrófono para transmitir.", Toast.LENGTH_LONG).show()
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        binding = ActivityMainBinding.inflate(layoutInflater)
        setContentView(binding.root)

        prefs = getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)

        setupAudioSpinners()
        loadSavedSettings()
        setupListeners()
        updateUiState(MicService.isRunning, MicService.isMuted)
    }

    override fun onResume() {
        super.onResume()
        refreshDeviceList()
        updateUiState(MicService.isRunning, MicService.isMuted)
        MicService.onStateChanged = { running, muted ->
            runOnUiThread { updateUiState(running, muted) }
        }
        MicService.onAudioLevel = { level ->
            runOnUiThread {
                binding.progressBarAudio.progress = level
            }
        }
    }

    override fun onPause() {
        super.onPause()
        MicService.onStateChanged = null
        MicService.onAudioLevel = null
    }

    private fun setupAudioSpinners() {
        val sourceOptions = listOf(
            AudioSourceOption(MediaRecorder.AudioSource.UNPROCESSED, "Estudio / Sin procesar (UNPROCESSED)"),
            AudioSourceOption(MediaRecorder.AudioSource.VOICE_RECOGNITION, "Voz Optimizada (VOICE_RECOGNITION)"),
            AudioSourceOption(MediaRecorder.AudioSource.MIC, "Mic Estándar (MIC)"),
            AudioSourceOption(MediaRecorder.AudioSource.VOICE_COMMUNICATION, "Comunicación / VoIP (VOICE_COMMUNICATION)")
        )
        val sourceAdapter = ArrayAdapter(this, R.layout.spinner_item, sourceOptions)
        sourceAdapter.setDropDownViewResource(R.layout.spinner_dropdown_item)
        binding.spAudioSource.adapter = sourceAdapter

        val savedSource = prefs.getInt(KEY_AUDIO_SOURCE, MediaRecorder.AudioSource.UNPROCESSED)
        val sourceIndex = sourceOptions.indexOfFirst { it.source == savedSource }.let { if (it >= 0) it else 0 }
        binding.spAudioSource.setSelection(sourceIndex)

        refreshDeviceList()
    }

    private fun refreshDeviceList() {
        val audioManager = getSystemService(Context.AUDIO_SERVICE) as? AudioManager
        val deviceList = mutableListOf<AudioDeviceOption>()
        deviceList.add(AudioDeviceOption(-1, "Automático / Por defecto"))

        if (audioManager != null) {
            val devices = audioManager.getDevices(AudioManager.GET_DEVICES_INPUTS)
            for (dev in devices) {
                val typeName = when (dev.type) {
                    AudioDeviceInfo.TYPE_BUILTIN_MIC -> "Micrófono Integrado"
                    AudioDeviceInfo.TYPE_WIRED_HEADSET -> "Auricular con Cable"
                    AudioDeviceInfo.TYPE_BLUETOOTH_SCO -> "Bluetooth"
                    AudioDeviceInfo.TYPE_USB_DEVICE, AudioDeviceInfo.TYPE_USB_HEADSET -> "USB"
                    else -> "Entrada de Audio"
                }
                val name = if (dev.productName.isNotEmpty()) dev.productName.toString() else typeName
                val label = "$typeName ($name)"
                deviceList.add(AudioDeviceOption(dev.id, label))
            }
        }

        val deviceAdapter = ArrayAdapter(this, R.layout.spinner_item, deviceList)
        deviceAdapter.setDropDownViewResource(R.layout.spinner_dropdown_item)
        binding.spAudioDevice.adapter = deviceAdapter

        val savedDeviceId = prefs.getInt(KEY_DEVICE_ID, -1)
        val deviceIndex = deviceList.indexOfFirst { it.id == savedDeviceId }.let { if (it >= 0) it else 0 }
        binding.spAudioDevice.setSelection(deviceIndex)
    }

    private fun gainFactorForStep(step: Int): Float = when (step) {
        0 -> 1.0f
        1 -> 1.5f
        2 -> 2.0f
        3 -> 2.5f
        4 -> 3.0f
        else -> 1.5f
    }

    private fun loadSavedSettings() {
        binding.etHost.setText(prefs.getString(KEY_HOST, ""))
        binding.etPort.setText(prefs.getInt(KEY_PORT, 47831).toString())
        binding.etToken.setText(prefs.getString(KEY_TOKEN, ""))

        val savedStep = prefs.getInt(KEY_GAIN_STEP, 1)
        binding.seekBarGain.progress = savedStep
        val factor = gainFactorForStep(savedStep)
        binding.tvGain.text = "Ganancia: ${factor}x"
        MicService.gainFactor = factor
    }

    private fun saveSettings(host: String, port: Int, token: String) {
        prefs.edit()
            .putString(KEY_HOST, host)
            .putInt(KEY_PORT, port)
            .putString(KEY_TOKEN, token)
            .apply()
    }

    private fun setupListeners() {
        binding.seekBarGain.setOnSeekBarChangeListener(object : SeekBar.OnSeekBarChangeListener {
            override fun onProgressChanged(seekBar: SeekBar?, progress: Int, fromUser: Boolean) {
                val factor = gainFactorForStep(progress)
                binding.tvGain.text = "Ganancia: ${factor}x"
                MicService.gainFactor = factor
                if (fromUser) {
                    prefs.edit().putInt(KEY_GAIN_STEP, progress).apply()
                }
            }
            override fun onStartTrackingTouch(seekBar: SeekBar?) {}
            override fun onStopTrackingTouch(seekBar: SeekBar?) {}
        })

        binding.btnScanQr.setOnClickListener {
            launchQrScanner()
        }

        binding.btnToggleStream.setOnClickListener {
            if (MicService.isRunning) {
                stopMicService()
            } else {
                checkPermissionsAndStart()
            }
        }

        binding.btnMute.setOnClickListener {
            val intent = Intent(this, MicService::class.java).apply {
                action = MicService.ACTION_TOGGLE_MUTE
            }
            startService(intent)
        }
    }

    private fun launchQrScanner() {
        val options = GmsBarcodeScannerOptions.Builder()
            .setBarcodeFormats(Barcode.FORMAT_QR_CODE)
            .enableAutoZoom()
            .build()

        val scanner = GmsBarcodeScanning.getClient(this, options)
        scanner.startScan()
            .addOnSuccessListener { barcode: Barcode ->
                val raw = barcode.rawValue
                if (!raw.isNullOrBlank()) {
                    parseQrContent(raw)
                }
            }
            .addOnFailureListener { e: Exception ->
                Toast.makeText(this, "No se pudo escanear: ${e.message}", Toast.LENGTH_SHORT).show()
            }
    }

    private fun parseQrContent(raw: String) {
        try {
            val uri = Uri.parse(raw)
            val host = uri.host ?: ""
            val port = if (uri.port > 0) uri.port else 47831
            val token = uri.getQueryParameter("token") ?: ""

            if (host.isNotEmpty()) {
                binding.etHost.setText(host)
                binding.etPort.setText(port.toString())
                binding.etToken.setText(token)
                saveSettings(host, port, token)
                Toast.makeText(this, "¡Conexión configurada por QR!", Toast.LENGTH_SHORT).show()

                if (!MicService.isRunning) {
                    checkPermissionsAndStart()
                }
            } else {
                Toast.makeText(this, "QR no reconocido como VoxMesh.", Toast.LENGTH_LONG).show()
            }
        } catch (e: Exception) {
            Toast.makeText(this, "Error al interpretar QR: ${e.message}", Toast.LENGTH_SHORT).show()
        }
    }

    private fun checkPermissionsAndStart() {
        val permissionsToRequest = mutableListOf<String>()

        if (ContextCompat.checkSelfPermission(this, Manifest.permission.RECORD_AUDIO) != PackageManager.PERMISSION_GRANTED) {
            permissionsToRequest.add(Manifest.permission.RECORD_AUDIO)
        }

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            if (ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
                permissionsToRequest.add(Manifest.permission.POST_NOTIFICATIONS)
            }
        }

        if (permissionsToRequest.isNotEmpty()) {
            permissionLauncher.launch(permissionsToRequest.toTypedArray())
        } else {
            startMicService()
        }
    }

    private fun startMicService() {
        val host = binding.etHost.text.toString().trim()
        val port = binding.etPort.text.toString().trim().toIntOrNull() ?: 47831
        val token = binding.etToken.text.toString().trim()

        if (host.isEmpty()) {
            Toast.makeText(this, "Por favor ingresá la IP de tu PC o escaneá el QR.", Toast.LENGTH_SHORT).show()
            return
        }

        val selectedSourceOpt = binding.spAudioSource.selectedItem as? AudioSourceOption
        val selectedSource = selectedSourceOpt?.source ?: MediaRecorder.AudioSource.UNPROCESSED

        val selectedDeviceOpt = binding.spAudioDevice.selectedItem as? AudioDeviceOption
        val selectedDeviceId = selectedDeviceOpt?.id ?: -1

        saveSettings(host, port, token)
        prefs.edit()
            .putInt(KEY_AUDIO_SOURCE, selectedSource)
            .putInt(KEY_DEVICE_ID, selectedDeviceId)
            .apply()

        val intent = Intent(this, MicService::class.java).apply {
            action = MicService.ACTION_START
            putExtra(MicService.EXTRA_HOST, host)
            putExtra(MicService.EXTRA_PORT, port)
            putExtra(MicService.EXTRA_TOKEN, token)
            putExtra(MicService.EXTRA_AUDIO_SOURCE, selectedSource)
            putExtra(MicService.EXTRA_DEVICE_ID, selectedDeviceId)
        }

        ContextCompat.startForegroundService(this, intent)
    }

    private fun stopMicService() {
        val intent = Intent(this, MicService::class.java).apply {
            action = MicService.ACTION_STOP
        }
        startService(intent)
    }

    private fun updateUiState(running: Boolean, muted: Boolean) {
        if (running) {
            binding.btnToggleStream.text = getString(R.string.stop_streaming)
            binding.btnToggleStream.background = ContextCompat.getDrawable(this, R.drawable.bg_button_red)
            binding.btnToggleStream.setTextColor(ContextCompat.getColor(this, R.color.text_primary))

            binding.btnMute.isEnabled = true
            binding.btnMute.text = if (muted) getString(R.string.unmute) else getString(R.string.mute)

            binding.tvStatus.text = if (muted) getString(R.string.status_muted) else getString(R.string.status_streaming)
            binding.tvStatus.setTextColor(ContextCompat.getColor(this, if (muted) R.color.accent_red else R.color.accent_green))

            binding.etHost.isEnabled = false
            binding.etPort.isEnabled = false
            binding.etToken.isEnabled = false
            binding.btnScanQr.isEnabled = false
            binding.spAudioSource.isEnabled = false
            binding.spAudioDevice.isEnabled = false
        } else {
            binding.btnToggleStream.text = getString(R.string.start_streaming)
            binding.btnToggleStream.background = ContextCompat.getDrawable(this, R.drawable.bg_button_green)
            binding.btnToggleStream.setTextColor(ContextCompat.getColor(this, R.color.bg_main))

            binding.btnMute.isEnabled = false
            binding.btnMute.text = getString(R.string.mute)

            binding.tvStatus.text = getString(R.string.status_disconnected)
            binding.tvStatus.setTextColor(ContextCompat.getColor(this, R.color.text_secondary))

            binding.progressBarAudio.progress = 0

            binding.etHost.isEnabled = true
            binding.etPort.isEnabled = true
            binding.etToken.isEnabled = true
            binding.btnScanQr.isEnabled = true
            binding.spAudioSource.isEnabled = true
            binding.spAudioDevice.isEnabled = true
        }
    }
}
