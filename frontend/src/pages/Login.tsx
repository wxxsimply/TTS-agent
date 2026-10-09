import { LineChartOutlined, LockOutlined, MailOutlined, SafetyCertificateOutlined } from '@ant-design/icons'
import { Alert, Button, Card, Form, Input, Modal, Tabs, Typography } from 'antd'
import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { authApi, getApiErrorMessage } from '../api/client'

type PasswordFormValues = {
  email: string
  password: string
}

type EmailCodeFormValues = {
  email: string
  code: string
}

type PasswordSetupValues = {
  password: string
  confirmPassword: string
}

export default function Login() {
  const navigate = useNavigate()
  const [loading, setLoading] = useState(false)
  const [sendingCode, setSendingCode] = useState(false)
  const [countdown, setCountdown] = useState(0)
  const [error, setError] = useState('')
  const [passwordSetupOpen, setPasswordSetupOpen] = useState(false)
  const [passwordSetupLoading, setPasswordSetupLoading] = useState(false)
  const [passwordSetupError, setPasswordSetupError] = useState('')
  const [emailForm] = Form.useForm<EmailCodeFormValues>()

  useEffect(() => {
    if (countdown <= 0) return
    const timer = window.setTimeout(() => setCountdown((current) => current - 1), 1000)
    return () => window.clearTimeout(timer)
  }, [countdown])

  const saveTokensAndEnter = (response: { data: { access_token: string; refresh_token: string; password_configured?: boolean } }) => {
    localStorage.setItem('access_token', response.data.access_token)
    localStorage.setItem('refresh_token', response.data.refresh_token)
    if (response.data.password_configured === false) {
      setPasswordSetupOpen(true)
      return
    }
    navigate('/chat')
  }

  const handleSetPassword = async (values: PasswordSetupValues) => {
    setPasswordSetupLoading(true)
    setPasswordSetupError('')
    try {
      await authApi.setPassword(values.password)
      setPasswordSetupOpen(false)
      navigate('/chat')
    } catch (setupError) {
      setPasswordSetupError(getApiErrorMessage(setupError, '密码设置失败，请稍后重试'))
    } finally {
      setPasswordSetupLoading(false)
    }
  }

  const handlePasswordAuth = async (values: PasswordFormValues, isRegister: boolean) => {
    setLoading(true)
    setError('')
    try {
      const response = isRegister
        ? await authApi.register(values.email, values.password)
        : await authApi.login(values.email, values.password)
      saveTokensAndEnter(response)
    } catch (authError: unknown) {
      setError(getApiErrorMessage(authError, isRegister ? '注册失败，请稍后重试' : '登录失败，请稍后重试'))
    } finally {
      setLoading(false)
    }
  }

  const handleSendCode = async () => {
    try {
      const values = await emailForm.validateFields(['email'])
      setSendingCode(true)
      setError('')
      const response = await authApi.sendEmailCode(values.email)
      setCountdown(response.data.resend_in_seconds || 60)
    } catch (sendError: unknown) {
      if (sendError && typeof sendError === 'object' && 'errorFields' in sendError) return
      setError(getApiErrorMessage(sendError, '验证码发送失败，请稍后重试'))
    } finally {
      setSendingCode(false)
    }
  }

  const handleEmailCodeLogin = async (values: EmailCodeFormValues) => {
    setLoading(true)
    setError('')
    try {
      const response = await authApi.loginWithEmailCode(values.email, values.code)
      saveTokensAndEnter(response)
    } catch (authError: unknown) {
      setError(getApiErrorMessage(authError, '验证码登录失败，请稍后重试'))
    } finally {
      setLoading(false)
    }
  }

  const passwordForm = (isRegister: boolean) => (
    <Form<PasswordFormValues> onFinish={(values) => handlePasswordAuth(values, isRegister)} layout="vertical" requiredMark={false}>
      <Form.Item name="email" rules={[{ required: true, type: 'email', message: '请输入有效邮箱' }]}>
        <Input prefix={<MailOutlined />} placeholder="邮箱" size="large" autoComplete="email" />
      </Form.Item>
      <Form.Item name="password" rules={[{ required: true, min: 6, message: '密码至少需要 6 位' }]}>
        <Input.Password prefix={<LockOutlined />} placeholder="密码" size="large" autoComplete={isRegister ? 'new-password' : 'current-password'} />
      </Form.Item>
      {error && <Alert type="error" message={error} showIcon style={{ marginBottom: 16 }} />}
      <Button type="primary" htmlType="submit" block size="large" loading={loading}>
        {isRegister ? '使用密码注册' : '密码登录'}
      </Button>
    </Form>
  )

  const emailCodeForm = (
    <Form<EmailCodeFormValues> form={emailForm} onFinish={handleEmailCodeLogin} layout="vertical" requiredMark={false}>
      <Form.Item name="email" rules={[{ required: true, type: 'email', message: '请输入有效邮箱' }]}>
        <Input prefix={<MailOutlined />} placeholder="邮箱" size="large" autoComplete="email" />
      </Form.Item>
      <Form.Item name="code" rules={[{ required: true, pattern: /^\d{6}$/, message: '请输入 6 位数字验证码' }]}>
        <Input
          prefix={<SafetyCertificateOutlined />}
          placeholder="6 位验证码"
          size="large"
          inputMode="numeric"
          maxLength={6}
          addonAfter={<Button type="link" size="small" loading={sendingCode} disabled={countdown > 0} onClick={handleSendCode}>{countdown > 0 ? `${countdown} 秒后重发` : '发送验证码'}</Button>}
        />
      </Form.Item>
      <Typography.Paragraph type="secondary" className="email-login-hint">首次验证邮箱会自动创建账号；验证码 10 分钟内有效。</Typography.Paragraph>
      {error && <Alert type="error" message={error} showIcon style={{ marginBottom: 16 }} />}
      <Button type="primary" htmlType="submit" block size="large" loading={loading}>
        邮箱验证码登录
      </Button>
    </Form>
  )

  return (
    <div className="login-shell">
      <Card className="login-panel">
        <div className="login-brand">
          <span className="login-symbol"><LineChartOutlined /></span>
          <Typography.Title level={3}>经营分析助手</Typography.Title>
          <Typography.Paragraph type="secondary">企业级智能报表与数据分析</Typography.Paragraph>
        </div>
        <Tabs
          onChange={() => setError('')}
          items={[
            { key: 'email-code', label: '邮箱验证码', children: emailCodeForm },
            { key: 'password', label: '密码登录', children: passwordForm(false) },
            { key: 'register', label: '密码注册', children: passwordForm(true) },
          ]}
        />
      </Card>
      <Modal
        title="设置登录密码"
        open={passwordSetupOpen}
        closable={false}
        maskClosable={false}
        keyboard={false}
        footer={null}
      >
        <Typography.Paragraph type="secondary">
          你的邮箱已经验证成功。请设置密码，以后既可以使用密码登录，也可以继续使用验证码登录。
        </Typography.Paragraph>
        <Form<PasswordSetupValues> layout="vertical" requiredMark={false} onFinish={handleSetPassword}>
          <Form.Item name="password" label="新密码" rules={[{ required: true, min: 6, max: 72, message: '密码需要 6 至 72 位' }]}> 
            <Input.Password prefix={<LockOutlined />} autoComplete="new-password" placeholder="请输入新密码" />
          </Form.Item>
          <Form.Item
            name="confirmPassword"
            label="确认密码"
            dependencies={['password']}
            rules={[
              { required: true, message: '请再次输入密码' },
              ({ getFieldValue }) => ({
                validator(_, value) {
                  return !value || getFieldValue('password') === value ? Promise.resolve() : Promise.reject(new Error('两次输入的密码不一致'))
                },
              }),
            ]}
          >
            <Input.Password prefix={<LockOutlined />} autoComplete="new-password" placeholder="请再次输入新密码" />
          </Form.Item>
          {passwordSetupError && <Alert type="error" message={passwordSetupError} showIcon style={{ marginBottom: 16 }} />}
          <Button type="primary" htmlType="submit" block loading={passwordSetupLoading}>保存密码并进入系统</Button>
        </Form>
      </Modal>
    </div>
  )
}
