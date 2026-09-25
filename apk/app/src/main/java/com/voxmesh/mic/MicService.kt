package com.voxmesh.mic

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.media.AudioFormat
import android.media.AudioRecord
import android.media.MediaRecorder
import android.os.Build
import android.os.IBinder
import android.os.PowerManager
import android.os.Process
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetAddress
import kotlin.concurrent.thread
import kotlin.math.sqrt

class MicService : Service() {

    companion object {
        const val ACTION_START = "com.voxmesh.mic.START"
        const val ACTION_STOP = "com.voxmesh.mic.STOP"
        const val ACTION_TOGGLE_MUTE = "com.voxmesh.mic.TOGGLE_MUTE"

        const val EXTRA_HOST = "extra_host"
        const val EXTRA_PORT = "extra_port"
        const val EXTRA_TOKEN = "extra_token"

        private const val NOTIFICATION_ID = 47831
        private const val CHANNEL_ID = "voxmesh_mic_channel"

        const val SAMPLE_RATE = 48000
        const val FRAME_SAMPLES = 960 // 20 ms a 48 kHz
        const val FRAME_BYTES = FRAME_SAMPLES * 2 // 1920 bytes

        @Volatile
        var isRunning: Boolean = false
            private set

        @Volatile
        var isMuted: Boolean = false
            private set

        var onStateChanged: ((running: Boolean, muted: Boolean) -> Unit)? = null
        var onAudioLevel: ((level: Int) -> Unit)? = null
    }

    private var host: String = ""
    private var port: Int = 47831
    private var token: String = ""

    private var audioRecord: AudioRecord? = null
    private var socket: DatagramSocket? = null
    private var wakeLock: PowerManager.WakeLock? = null
    private var streamThread: Thread? = null

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val action = intent?.action ?: return START_NOT_STICKY

        when (action) {
            ACTION_START -> {
                host = intent.getStringExtra(EXTRA_HOST) ?: ""
                port = intent.getIntExtra(EXTRA_PORT, 47831)
                token = intent.getStringExtra(EXTRA_TOKEN) ?: ""
                if (host.isNotEmpty() && !isRunning) {
                    startStreaming()
                }
            }
            ACTION_STOP -> {
                stopStreaming()
                stopSelf()
            }
            ACTION_TOGGLE_MUTE -> {
                isMuted = !isMuted
                updateNotification()
                onStateChanged?.invoke(isRunning, isMuted)
            }
        }

        return START_NOT_STICKY
    }

    private fun startStreaming() {
        createNotificationChannel()
        val notification = buildNotification()
        
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            ServiceCompat.startForeground(
                this,
                NOTIFICATION_ID,
                notification,
                ServiceInfo.FOREGROUND_SERVICE_TYPE_MICROPHONE
            )
        } else {
            startForeground(NOTIFICATION_ID, notification)
        }

        acquireWakeLock()

        isRunning = true
        onStateChanged?.invoke(true, isMuted)

        streamThread = thread(name = "VoxMeshUdpMicThread", priority = Process.THREAD_PRIORITY_URGENT_AUDIO) {
            runAudioLoop()
        }
    }

    private fun runAudioLoop() {
        var record: AudioRecord? = null
        var udpSocket: DatagramSocket? = null

        try {
            val minBuf = AudioRecord.getMinBufferSize(
                SAMPLE_RATE,
                AudioFormat.CHANNEL_IN_MONO,
                AudioFormat.ENCODING_PCM_16BIT
            )
            val bufferSize = minBuf.coerceAtLeast(FRAME_BYTES * 4)

            record = try {
                AudioRecord(
                    MediaRecorder.AudioSource.VOICE_COMMUNICATION,
                    SAMPLE_RATE,
                    AudioFormat.CHANNEL_IN_MONO,
                    AudioFormat.ENCODING_PCM_16BIT,
                    bufferSize
                )
            } catch (e: Exception) {
                AudioRecord(
                    MediaRecorder.AudioSource.MIC,
                    SAMPLE_RATE,
                    AudioFormat.CHANNEL_IN_MONO,
                    AudioFormat.ENCODING_PCM_16BIT,
                    bufferSize
                )
            }

            if (record.state != AudioRecord.STATE_INITIALIZED) {
                record.release()
                record = AudioRecord(
                    MediaRecorder.AudioSource.MIC,
                    SAMPLE_RATE,
                    AudioFormat.CHANNEL_IN_MONO,
                    AudioFormat.ENCODING_PCM_16BIT,
                    bufferSize
                )
            }

            audioRecord = record
            record.startRecording()

            udpSocket = DatagramSocket()
            socket = udpSocket
            val destAddress = InetAddress.getByName(host)

            val tokenBytes = token.toByteArray(Charsets.UTF_8)
            val tokenLen = tokenBytes.size.coerceAtMost(255).toByte()
            val headerSize = 5 + tokenBytes.size
            val packetData = ByteArray(headerSize + FRAME_BYTES)

            // Header: "VMIC" (4 bytes) + tokenLen (1 byte) + tokenBytes
            packetData[0] = 'V'.code.toByte()
            packetData[1] = 'M'.code.toByte()
            packetData[2] = 'I'.code.toByte()
            packetData[3] = 'C'.code.toByte()
            packetData[4] = tokenLen
            System.arraycopy(tokenBytes, 0, packetData, 5, tokenBytes.size)

            val pcmChunk = ByteArray(FRAME_BYTES)
            val packet = DatagramPacket(packetData, packetData.size, destAddress, port)

            while (isRunning) {
                var bytesRead = 0
                while (bytesRead < FRAME_BYTES && isRunning) {
                    val read = record.read(pcmChunk, bytesRead, FRAME_BYTES - bytesRead)
                    if (read > 0) {
                        bytesRead += read
                    } else {
                        break
                    }
                }

                if (bytesRead == FRAME_BYTES) {
                    val level = calculateAudioLevel(pcmChunk)
                    onAudioLevel?.invoke(level)

                    if (!isMuted) {
                        System.arraycopy(pcmChunk, 0, packetData, headerSize, FRAME_BYTES)
                    } else {
                        java.util.Arrays.fill(packetData, headerSize, headerSize + FRAME_BYTES, 0.toByte())
                    }

                    udpSocket.send(packet)
                }
            }
        } catch (e: Exception) {
            e.printStackTrace()
        } finally {
            try {
                record?.stop()
                record?.release()
            } catch (_: Exception) {}
            try {
                udpSocket?.close()
            } catch (_: Exception) {}
            audioRecord = null
            socket = null
        }
    }

    private fun calculateAudioLevel(pcm: ByteArray): Int {
        var sum = 0.0
        val sampleCount = pcm.size / 2
        for (i in 0 until sampleCount) {
            val sample = (pcm[i * 2].toInt() and 0xFF) or (pcm[i * 2 + 1].toInt() shl 8)
            val signed = sample.toShort().toDouble()
            sum += signed * signed
        }
        val rms = sqrt(sum / sampleCount)
        val normalized = (rms / 32768.0 * 200.0).coerceIn(0.0, 100.0)
        return normalized.toInt()
    }

    private fun stopStreaming() {
        isRunning = false
        isMuted = false
        streamThread?.interrupt()
        streamThread = null

        releaseWakeLock()

        try {
            audioRecord?.stop()
            audioRecord?.release()
        } catch (_: Exception) {}
        audioRecord = null

        try {
            socket?.close()
        } catch (_: Exception) {}
        socket = null

        stopForeground(STOP_FOREGROUND_REMOVE)
        onStateChanged?.invoke(false, false)
        onAudioLevel?.invoke(0)
    }

    private fun acquireWakeLock() {
        if (wakeLock == null) {
            val powerManager = getSystemService(Context.POWER_SERVICE) as PowerManager
            wakeLock = powerManager.newWakeLock(
                PowerManager.PARTIAL_WAKE_LOCK,
                "VoxMeshMic::StreamingWakeLock"
            ).apply {
                acquire(12 * 60 * 60 * 1000L)
            }
        }
    }

    private fun releaseWakeLock() {
        try {
            if (wakeLock?.isHeld == true) {
                wakeLock?.release()
            }
        } catch (_: Exception) {}
        wakeLock = null
    }

    private fun createNotificationChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val channel = NotificationChannel(
                CHANNEL_ID,
                getString(R.string.notification_channel_name),
                NotificationManager.IMPORTANCE_LOW
            ).apply {
                description = getString(R.string.notification_channel_desc)
                setShowBadge(false)
            }
            val manager = getSystemService(NotificationManager::class.java)
            manager.createNotificationChannel(channel)
        }
    }

    private fun buildNotification(): Notification {
        val stopIntent = Intent(this, MicService::class.java).apply { action = ACTION_STOP }
        val stopPending = PendingIntent.getService(
            this, 1, stopIntent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )

        val muteIntent = Intent(this, MicService::class.java).apply { action = ACTION_TOGGLE_MUTE }
        val mutePending = PendingIntent.getService(
            this, 2, muteIntent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )

        val mainIntent = Intent(this, MainActivity::class.java)
        val mainPending = PendingIntent.getActivity(
            this, 0, mainIntent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )

        val statusText = if (isMuted) "Micrófono Silenciado" else "Transmitiendo audio a $host:$port"
        val muteActionTitle = if (isMuted) getString(R.string.action_unmute) else getString(R.string.action_mute)

        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setSmallIcon(R.drawable.ic_mic)
            .setContentTitle(getString(R.string.notification_title))
            .setContentText(statusText)
            .setContentIntent(mainPending)
            .setOngoing(true)
            .addAction(0, muteActionTitle, mutePending)
            .addAction(0, getString(R.string.action_stop), stopPending)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .build()
    }

    private fun updateNotification() {
        val manager = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        manager.notify(NOTIFICATION_ID, buildNotification())
    }

    override fun onDestroy() {
        stopStreaming()
        super.onDestroy()
    }
}
