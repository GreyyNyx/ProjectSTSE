pipeline {
    agent any

    environment {
        DB_NAME = 'footballhub_ci'
        DB_USER = 'ci_user'
        DB_PASSWORD = 'ci_password'
    }

    stages {
        stage('Validate Docker Compose') {
            steps {
                echo 'Memeriksa konfigurasi Docker Compose...'
                sh 'docker compose config -q'
            }
        }

        stage('Build Docker Images') {
            steps {
                echo 'Membangun Docker image Football Hub...'
                sh 'docker compose build'
            }
        }
    }

    post {
        success {
            echo 'Pipeline Football Hub berhasil!'
        }

        failure {
            echo 'Pipeline Football Hub gagal. Periksa Console Output.'
        }
    }
}
